package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
)

// Kind is what a routine runs: a model (an LLM routine, what "routine" means
// unless a kind is named) or an executable (a program).
type Kind string

// Kinds of routine.
const (
	KindLLM     Kind = "llm"
	KindProgram Kind = "program"
)

// Kinds are every kind, the default first.
var Kinds = []Kind{KindLLM, KindProgram}

// Written is the kind as files and outputs write it: absent for an LLM
// routine, so what was written before programs existed reads the same.
func (k Kind) Written() string {
	if k == KindLLM {
		return ""
	}
	return string(k)
}

// ParseKind reads a kind as written: empty is llm.
func ParseKind(s string) (Kind, error) {
	if s == "" {
		return KindLLM, nil
	}
	for _, k := range Kinds {
		if Kind(s) == k {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown kind %q: llm or program", s)
}

// Runnable is a routine of either kind, as the machinery they share sees it:
// the scheduler, the run queue and the routine's own lock, catch-up and
// staleness, waiting for a full wake, keeping the computer awake, pausing,
// stopping, trust, run status, history and notifications. Each kind
// implements it in its own file (llm.go, program.go); nothing else branches
// on a routine's kind.
type Runnable interface {
	Name() string
	Kind() Kind
	// Path is its file: an LLM routine's .prompt file, a program's
	// description file.
	Path() string
	Description() string
	// Source is its file as written.
	Source() string
	// Settings are its per-routine settings (routines.<name>.<field>), with
	// defaults from its file.
	Settings() []settings.Setting

	// runConfig resolves the settings every kind has.
	runConfig(a *App) (RunConfig, error)
	// problems keep it from running whether trusted or not, as far as can be
	// told without connecting to anything (its config resolved).
	problems(a *App) []Problem
	// check adds what connecting finds (an LLM routine's servers, tools and
	// model).
	check(ctx context.Context, a *App, rd *Readiness)
	// reaches are the sign-ins it needs (`auth login` names).
	reaches(a *App) []string
	// approval is what trusting it approves, as it is now.
	approval(a *App) (trust.Approval, error)
	// run runs it once it has its turn (App.Run does the rest).
	run(ctx context.Context, a *App, rc runContext) (*RunResult, error)
}

// runContext is what a kind's run is told.
type runContext struct {
	occ     trigger.Occurrence
	started time.Time
	dryRun  bool
	log     io.Writer
	cfg     RunConfig
	// id is the run's history ID.
	id string
	// note changes what run status shows of the run while it runs (its
	// session, backend); dry runs show nothing.
	note func(func(*runstatus.Run))
}

// RunResult is how a run went: what every kind has, and its kind's own
// section.
type RunResult struct {
	Kind Kind
	// Inputs are what an LLM routine's prompt was given (today, since, ...).
	Inputs map[string]any
	// Agent is an LLM routine's: what the model did.
	Agent *agent.Result
	// Program is a program's: its exit code, summary and output.
	Program *programs.Execution
	// Note is what a successful run's record says ("nothing to do: …", a
	// program's summary); empty when there's nothing to say.
	Note string
	// Line is the run in one line, for logs.
	Line string
}

// Broken is a routine that doesn't load, so it can't run: its file is
// broken, or its name is taken by both kinds.
type Broken struct {
	Kind Kind
	Name string
	Path string
	Err  error
}

func (b Broken) Error() string { return fmt.Sprintf("%s: %v", b.Path, b.Err) }

// All is every routine of the kinds asked for (every kind when none are),
// sorted by name, and those that don't load. A name both kinds have is
// broken for both: neither runs until one is deleted.
func (a *App) All(kinds ...Kind) ([]Runnable, []Broken, error) {
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return nil, nil, err
	}
	progs, err := programs.Load(a.Layout.Programs())
	if err != nil {
		return nil, nil, err
	}
	var all []Runnable
	var broken []Broken
	for _, r := range set.Routines {
		all = append(all, a.LLM(r, set))
	}
	for _, e := range set.Errors {
		name := strings.TrimSuffix(fileBase(e.Path), routines.Ext)
		broken = append(broken, Broken{Kind: KindLLM, Name: name, Path: e.Path, Err: e.Err})
	}
	for _, p := range progs.Programs {
		all = append(all, &program{p: p})
	}
	for _, e := range progs.Errors {
		broken = append(broken, Broken{Kind: KindProgram, Name: e.Name, Path: e.Path, Err: e.Err})
	}
	// A name taken twice: both are broken.
	count := map[string]int{}
	for _, x := range all {
		count[x.Name()]++
	}
	for _, b := range broken {
		count[b.Name]++
	}
	var kept []Runnable
	for _, x := range all {
		if count[x.Name()] > 1 {
			broken = append(broken, Broken{Kind: x.Kind(), Name: x.Name(), Path: x.Path(), Err: bothKinds(x.Name())})
			continue
		}
		kept = append(kept, x)
	}
	for i, b := range broken {
		if count[b.Name] > 1 {
			broken[i].Err = bothKinds(b.Name)
		}
	}
	keep := func(k Kind) bool {
		if len(kinds) == 0 {
			return true
		}
		for _, want := range kinds {
			if want == k {
				return true
			}
		}
		return false
	}
	out := kept[:0]
	for _, x := range kept {
		if keep(x.Kind()) {
			out = append(out, x)
		}
	}
	var outBroken []Broken
	for _, b := range broken {
		if keep(b.Kind) {
			outBroken = append(outBroken, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	sort.Slice(outBroken, func(i, j int) bool { return outBroken[i].Name < outBroken[j].Name })
	return out, outBroken, nil
}

func bothKinds(name string) error {
	return fmt.Errorf("%s is both an LLM routine and a program: delete one (neither runs until then)", name)
}

// Find is the routine of either kind named name: commands that take a name
// find it whatever its kind.
func (a *App) Find(name string) (Runnable, error) {
	all, broken, err := a.All()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for _, x := range all {
		if x.Name() == name {
			return x, nil
		}
		names = append(names, x.Name())
	}
	for _, b := range broken {
		if b.Name == name {
			return nil, b
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no routine %q: there are no routines installed", name)
	}
	return nil, fmt.Errorf("no routine %q (have: %s)", name, strings.Join(names, ", "))
}

// NameTaken says whether a routine of either kind already has name (a new
// one may not take it), or "" when none does.
func (a *App) NameTaken(name string) string {
	if programs.Exists(a.Layout.Programs(), name) {
		return fmt.Sprintf("a program named %q already exists", name)
	}
	if fileExists(a.Layout.Routines(), name+routines.Ext) {
		return fmt.Sprintf("routine %q already exists", name)
	}
	return ""
}

// kindOf is the kind of the routine named name (an LLM routine unless a
// program has the name), for records made without loading it.
func (a *App) kindOf(name string) Kind {
	if programs.Exists(a.Layout.Programs(), name) {
		return KindProgram
	}
	return KindLLM
}

func fileBase(path string) string { return filepath.Base(path) }

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
