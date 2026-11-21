package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/schedule"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// SetupStep is one thing first-run setup covers.
type SetupStep struct {
	// Step is what it sets up.
	Step string `json:"step" jsonschema:"enum=templates,enum=backend,enum=routine,enum=sign-in,enum=turn-on,enum=usage,enum=mcp"`
	// Subject is the server or routine it's about (routine, sign-in, turn-on).
	Subject string `json:"subject,omitempty"`
	// Title names the step ("Model backend", "Sign in to tracker").
	Title string `json:"title"`
	Done  bool   `json:"done"`
	// Optional steps don't keep setup from being done: installing templates,
	// and resuming a trusted routine once another routine already runs on its
	// schedule (a paused one may be paused on purpose).
	Optional bool `json:"optional,omitempty"`
	// Auto steps are done without asking: the interactive setup and
	// frontends run its commands as soon as they come to it (connecting
	// Claude Code to the app's MCP server). Ask is empty.
	Auto bool `json:"auto,omitempty"`
	// Status says where it stands, and Ask what to ask me before doing it, in
	// words for me.
	Status string `json:"status"`
	Ask    string `json:"ask,omitempty"`
	// Prepare is what to do outside the app before signing in (a sign-in
	// step whose plugin needs it, e.g. creating a Slack app): numbered steps
	// to show, and have done, first. `auth login` shows them too.
	Prepare string `json:"prepare,omitempty"`
	// SignIn is what the step still needs signed in (`auth login <signIn>
	// --browser`): a server, or the API key the api backend needs.
	SignIn string `json:"signIn,omitempty"`
	// Setting is the setting the step chooses, for frontends to show and set
	// (backend: llm.backend).
	Setting string `json:"setting,omitempty"`
	// Commands do the step once I've answered: placeholders in <angle
	// brackets> are my answers, and a trailing # comment says when a command
	// applies or what to do with its output.
	Commands []string `json:"commands,omitempty"`
}

// SetupOutput is `setup --json`: every step, in order of kind (templates,
// backend, plan usage, Claude Code's MCP connection, routines to fix,
// sign-ins, routines to turn on).
type SetupOutput struct {
	Steps []SetupStep `json:"steps"`
	// Done is true when every step that isn't optional is; Remaining counts
	// those that aren't.
	Done      bool `json:"done"`
	Remaining int  `json:"remaining"`
}

// Setup step kinds.
const (
	stepTemplates = "templates"
	stepBackend   = "backend"
	stepRoutine   = "routine"
	stepSignIn    = "sign-in"
	stepTurnOn    = "turn-on"
	stepUsage     = "usage"
	stepMCP       = "mcp"
)

func newSetupCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided first run: templates, the model backend, sign-ins, and turning routines on",
		Long: "In a terminal, walks through each step and asks before doing anything. With --json it changes " +
			"nothing: it lists the steps, what's done, what to ask, and the commands that do each one, for an LLM " +
			"or a frontend guiding me. Run it again any time; done steps are skipped.",
		Annotations: map[string]string{annReadOnlyWith: "json", annNotReadOnly: "setting up the app is mine"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			if asJSON {
				out, err := setupSteps(a)
				if err != nil {
					return err
				}
				return writeJSON(cmd.OutOrStdout(), out)
			}
			if !isTerminal(os.Stdin) {
				return errors.New("setup asks questions: run it in a terminal, or see the steps with --json")
			}
			return setupInteractive(cmd, a)
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, SetupOutput{}).Usage = "list the steps and the commands for each, changing nothing"
	return cmd
}

// setupLeft is the setup steps still to do (not optional), for run status.
func setupLeft(a *engine.App) ([]string, error) {
	out, err := setupSteps(a)
	if err != nil {
		return nil, err
	}
	var left []string
	for _, st := range out.Steps {
		if !st.Done && !st.Optional {
			left = append(left, st.Title)
		}
	}
	return left, nil
}

// setupSteps is where setup stands. It only reads.
func setupSteps(a *engine.App) (SetupOutput, error) {
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return SetupOutput{}, err
	}
	backend, err := backendStep(a)
	if err != nil {
		return SetupOutput{}, err
	}
	steps := []SetupStep{templatesStep(set, templatesOffered(a)), backend}
	if st, ok, err := usageStep(a); err != nil {
		return SetupOutput{}, err
	} else if ok {
		steps = append(steps, st)
	}
	if st, ok, err := mcpStep(a); err != nil {
		return SetupOutput{}, err
	} else if ok {
		steps = append(steps, st)
	}
	loaded, problems := loadRoutines(a, set)
	steps = append(steps, problems...)
	signIns, err := signInSteps(a, loaded)
	if err != nil {
		return SetupOutput{}, err
	}
	steps = append(steps, signIns...)
	turnOns, err := turnOnSteps(a, loaded)
	if err != nil {
		return SetupOutput{}, err
	}
	steps = append(steps, turnOns...)

	out := SetupOutput{Steps: steps}
	for _, st := range steps {
		if !st.Done && !st.Optional {
			out.Remaining++
		}
	}
	out.Done = out.Remaining == 0
	return out, nil
}

func templatesStep(set *routines.Set, offered int) SetupStep {
	// Optional: the app is set up without any routines.
	st := SetupStep{Step: stepTemplates, Title: "Templates", Done: len(set.Routines) > 0, Optional: true, Status: "No routines installed yet",
		Ask: "First, on its own: which of the templates' projects do you work on (name each one, even if there's " +
			"only one)? Then, each template as its own choice: which should be installed as routines, of those of " +
			"your projects and those for any project? Templates with the same name are alternatives. None are " +
			"picked by default.",
		Commands: []string{
			product.Command + " templates list --json   # each template's project (none: for any project); offer only the ones of the projects they work on, and those with none",
			product.Command + " templates install <template> [--set <param>=<value>]...",
		}}
	if offered == 0 {
		st.Ask = "There are no templates to install. Write a routine of your own instead, or skip this step."
		st.Commands = []string{product.Command + " routines create <name> --from <file>   # a routine of their own, written with them"}
	}
	if st.Done {
		names := make([]string, 0, len(set.Routines))
		for _, r := range set.Routines {
			names = append(names, r.Name)
		}
		st.Status = "Routines installed: " + strings.Join(names, ", ")
	}
	return st
}

// templatesOffered counts the templates for this OS not installed yet; a
// build that doesn't know where its templates are has none.
func templatesOffered(a *engine.App) int {
	rows, err := templateRows(a, false)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range rows {
		if !t.Installed {
			n++
		}
	}
	return n
}

func backendStep(a *engine.App) (SetupStep, error) {
	b, err := a.BackendReady()
	if err != nil {
		return SetupStep{}, err
	}
	def, _ := settings.Lookup("llm.backend")
	st := SetupStep{Step: stepBackend, Title: "Model backend", Done: b.Problem == "", Status: b.Status(), SignIn: b.SignIn,
		Setting:  def.Key,
		Ask:      "Which backend should run routines? " + def.Description,
		Commands: []string{product.Command + " settings set " + def.Key + " <" + strings.Join(def.Enum, "|") + ">"}}
	if b.SignIn != "" {
		st.Commands = append(st.Commands, product.Command+" auth login "+b.SignIn+" --browser")
	}
	return st, nil
}

// usageStep is there while the app can't read the Claude plan's usage from
// Claude Code's /usage (claude-code only): it reads it from Claude Code's
// status line instead, once that runs the app's command.
func usageStep(a *engine.App) (SetupStep, bool, error) {
	def, err := a.BackendDef()
	if err != nil || def.PlanUsage == nil {
		return SetupStep{}, false, err
	}
	plan, err := usage.LoadPlan(a.Layout.PlanUsage())
	if err != nil || plan.TryError == "" {
		return SetupStep{}, false, err
	}
	settingsPath, err := statusline.SettingsPath()
	if err != nil {
		return SetupStep{}, false, err
	}
	installed, err := statusline.Installed(settingsPath)
	if err != nil {
		return SetupStep{}, false, err
	}
	st := SetupStep{Step: stepUsage, Title: "Plan usage", Done: installed,
		Status: "Read from your Claude Code status line (Claude Code's /usage can't be read)",
		Ask: "The app can't read your Claude plan's usage from Claude Code right now. Read it from your Claude Code " +
			"status line instead? Your status line keeps showing what it shows now.",
		Commands: []string{product.Command + " usage statusline install"}}
	if !installed {
		st.Status = "Can't be read from Claude Code's /usage: " + plan.TryError
	}
	return st, true, nil
}

// mcpStep is there on the claude-code backend: Claude Code gets the app's
// MCP server at user scope, so I can manage the app from any Claude Code
// session. Required, and done without asking (it only adds a server to
// Claude Code, which asks before each tool call that changes anything).
func mcpStep(a *engine.App) (SetupStep, bool, error) {
	def, err := a.BackendDef()
	if err != nil || def.Name != "claude-code" {
		return SetupStep{}, false, err
	}
	problem, err := claudeMCPState()
	if err != nil {
		return SetupStep{}, false, err
	}
	st := SetupStep{Step: stepMCP, Title: "Claude Code connection", Done: problem == "", Auto: true,
		Status:   "Claude Code has " + product.Command + "'s MCP server: /mcp lists it in every folder",
		Commands: []string{product.Command + " mcp connect"}}
	if problem != "" {
		st.Status = capitalize(problem)
	}
	return st, true, nil
}

// loadedRoutine is a routine whose settings resolve.
type loadedRoutine struct {
	routines.Routine
	cfg engine.RoutineConfig
}

// loadRoutines resolves each routine's settings; a routine file that doesn't
// load, settings that don't resolve, a missing folder or a server no plugin
// provides is a step to fix (it would fail every run).
func loadRoutines(a *engine.App, set *routines.Set) ([]loadedRoutine, []SetupStep) {
	var loaded []loadedRoutine
	var problems []SetupStep
	fix := func(name, problem string) {
		problems = append(problems, SetupStep{Step: stepRoutine, Subject: name, Title: "Fix " + name,
			Status:   capitalize(problem),
			Commands: []string{product.Command + " doctor   # says what's wrong and how to fix it"}})
	}
	for _, e := range set.Errors {
		fix(strings.TrimSuffix(filepath.Base(e.Path), routines.Ext), e.Err.Error())
	}
	for _, r := range set.Routines {
		rd := a.Assess(r)
		if rd.ConfigErr != nil {
			fix(r.Name, rd.ConfigErr.Error())
			continue
		}
		for _, p := range rd.Problems {
			fix(r.Name, p.Text)
		}
		loaded = append(loaded, loadedRoutine{r, rd.Config})
	}
	return loaded, problems
}

// signInSteps is one step per server the routines reach (through plugins
// too) that signs in, done or not.
func signInSteps(a *engine.App, loaded []loadedRoutine) ([]SetupStep, error) {
	rows, err := authStatuses(a)
	if err != nil {
		return nil, err
	}
	users := map[string][]string{} // server -> routines reaching it
	for _, r := range loaded {
		for _, s := range a.Servers(r.Routine) {
			users[s] = append(users[s], r.Name)
		}
	}
	var steps []SetupStep
	for _, row := range rows { // sorted by name
		who := users[row.Server]
		if len(who) == 0 {
			continue
		}
		st := SetupStep{Step: stepSignIn, Subject: row.Server, Title: "Sign in to " + row.Server, Done: row.SignedIn,
			Status:   "Signed in; used by " + strings.Join(who, ", "),
			Ask:      "Sign in to " + row.Server + "? It opens the browser.",
			Commands: []string{product.Command + " auth login " + row.Server + " --browser"}}
		if !row.SignedIn {
			st.Status, st.SignIn = "Not signed in; used by "+strings.Join(who, ", "), row.Server
			if p, ok := a.FindPlugin(row.Server); ok {
				st.Prepare = p.Prepare
			}
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// turnOnSteps is a step per routine: trusting what it can reach and, when
// it has a schedule, running on it, in one yes (`routines trust --resume`).
// It's done once the routine is trusted and isn't paused. Trusting is
// required; resuming a trusted routine is optional once another routine runs
// on its schedule (a paused one may be paused on purpose).
func turnOnSteps(a *engine.App, loaded []loadedRoutine) ([]SetupStep, error) {
	untrusted := map[string]string{}
	for _, r := range loaded {
		why, _, err := a.TrustProblem(r.Routine, r.cfg)
		if err != nil {
			return nil, err
		}
		if why != "" {
			untrusted[r.Name] = why
		}
	}
	runs := func(r loadedRoutine) bool { return r.cfg.Enabled && r.cfg.Schedule != "" && untrusted[r.Name] == "" }
	anyRuns := slices.ContainsFunc(loaded, runs)
	steps := make([]SetupStep, 0, len(loaded))
	for _, r := range loaded {
		why, words := untrusted[r.Name], schedule.Text(r.cfg.Schedule)
		paused := r.cfg.Schedule != "" && !r.cfg.Enabled
		st := SetupStep{Step: stepTurnOn, Subject: r.Name, Title: "Turn on " + r.Name, Done: why == "" && !paused,
			Commands: []string{product.Command + " routines trust " + r.Name + " --yes --resume"}}
		switch {
		case why != "":
			st.Status = capitalize(why)
			st.Ask = "Trust " + r.Name + " to run for real with what it can reach"
			switch {
			case paused:
				st.Status += "; paused (" + words + ")"
				st.Ask += ", and run it on its schedule (" + words + ")"
			case r.cfg.Schedule != "":
				st.Status += ", so it can't run on its schedule (" + words + ")"
			}
			st.Ask += "?"
			st.Commands = append([]string{product.Command + " routines trust " + r.Name + " --check --json   # show me what it can reach before asking"}, st.Commands...)
		case paused:
			st.Optional = anyRuns
			st.Status = "Trusted, but paused: it doesn't run on its schedule (" + words + ")"
			st.Ask = "Run " + r.Name + " on its schedule (" + words + ")?"
		case r.cfg.Schedule != "":
			st.Status = "Trusted; runs on its schedule: " + words
		default:
			st.Status = "Trusted; it has no schedule, so it runs when started"
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// capitalize starts a status with a capital, as every status does.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// setupInteractive walks through the steps in a terminal, asking before each:
// it installs templates, signs in and turns routines on through the same
// commands setup --json lists (run attached to the terminal, so their own
// questions work), and sets the backend directly.
func setupInteractive(cmd *cobra.Command, a *engine.App) error {
	w := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())
	ask := func(q string) string {
		fmt.Fprint(w, q+" ")
		line, _ := in.ReadString('\n')
		return strings.TrimSpace(line)
	}
	yes := func(q string, def bool) bool {
		hint := " [y/N]"
		if def {
			hint = " [Y/n]"
		}
		switch strings.ToLower(ask(q + hint)) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			return def
		}
	}
	// A command that fails says why itself, on the terminal.
	self := func(args ...string) { runSelf(a, cmd.InOrStdin(), w, cmd.ErrOrStderr(), args...) }

	fmt.Fprintln(w, "Setting up "+product.Name+". Nothing changes without your yes.")

	fmt.Fprintln(w, "\n1. Templates")
	if err := setupTemplates(a, w, ask, self); err != nil {
		fmt.Fprintln(w, "  Skipped:", err)
	}

	fmt.Fprintln(w, "\n2. Model backend")
	b, err := a.BackendReady()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "  "+b.Status())
	def, _ := settings.Lookup("llm.backend")
	fmt.Fprintln(w, "  "+def.Description)
	choice := strings.ToLower(ask("  Which backend (" + strings.Join(def.Enum, " or ") + ")? [Enter keeps " + b.Name + "]"))
	if choice != "" && choice != b.Name {
		if _, err := (settings.Store{Path: a.Layout.Config()}).Set("llm.backend", choice); err != nil {
			fmt.Fprintln(w, "  ", err)
		}
	}
	if b, err = a.BackendReady(); err != nil {
		return err
	}
	if b.SignIn != "" && yes("  "+capitalize(b.Problem)+". Sign in to "+b.SignIn+" now?", true) {
		self("auth", "login", b.SignIn)
	} else if b.Problem != "" {
		fmt.Fprintln(w, "  Still needed: "+b.Problem)
	}
	if st, ok, err := mcpStep(a); err != nil {
		return err
	} else if ok && !st.Done {
		fmt.Fprintln(w, "  Connecting Claude Code to "+product.Command+"'s MCP server:")
		self("mcp", "connect")
	}

	fmt.Fprintln(w, "\n3. Routines and sign-ins")
	steps, err := setupSteps(a)
	if err != nil {
		return err
	}
	for _, s := range steps.Steps {
		if s.Done {
			continue
		}
		switch s.Step {
		case stepRoutine:
			fmt.Fprintf(w, "  %s: %s (`"+product.Command+" doctor` says how to fix it)\n", s.Title, s.Status)
		case stepSignIn:
			if yes("  "+s.Status+". "+s.Ask, true) {
				self("auth", "login", s.Subject)
			}
		case stepUsage:
			if yes("  "+s.Ask, true) {
				self("usage", "statusline", "install")
			}
		case stepTurnOn:
			fmt.Fprintf(w, "  %s: %s\n", s.Title, s.Status)
			self("routines", "trust", s.Subject, "--resume") // shows what it can reach, then asks
		}
	}

	final, err := setupSteps(a)
	if err != nil {
		return err
	}
	if final.Done {
		fmt.Fprintln(w, "\nAll set. `"+product.Command+" routines list` shows your routines; `"+product.Command+" doctor` explains a red light.")
		return nil
	}
	fmt.Fprintln(w, "\nStill to do (run `"+product.Command+" setup` again any time):")
	for _, s := range final.Steps {
		if !s.Done && !s.Optional {
			fmt.Fprintf(w, "  - %s: %s\n", s.Title, s.Status)
		}
	}
	return nil
}

// setupTemplates offers the templates for this OS not installed yet; none
// are picked by default.
func setupTemplates(a *engine.App, w io.Writer, ask func(string) string, self func(...string)) error {
	rows, err := templateRows(a, false)
	if err != nil {
		return err
	}
	list := make([]templates.Template, 0, len(rows))
	for _, t := range rows {
		list = append(list, t.Template)
	}
	mine := map[string]bool{}
	if projects := templates.Projects(list); len(projects) > 0 {
		fmt.Fprintln(w, "  Projects with their own templates:")
		for i, p := range projects {
			fmt.Fprintf(w, "  %d. %s\n", i+1, p)
		}
		for _, n := range pickNumbers(w, ask("  Which do you work on? Numbers, comma-separated (Enter for none):"), len(projects)) {
			mine[projects[n]] = true
		}
	}
	var offer []TemplateInfo
	for _, t := range rows {
		if !t.Installed && (t.Project == "" || mine[t.Project]) {
			offer = append(offer, t)
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "  No templates to offer. Write a routine of your own with `"+product.Command+" routines create`.")
		return nil
	}
	if len(offer) == 0 {
		fmt.Fprintln(w, "  No templates to offer: those for your projects are installed.")
		return nil
	}
	fmt.Fprintln(w, "  Templates for your projects, and for any project:")
	for i, t := range offer {
		fmt.Fprintf(w, "  %d. %s: %s\n", i+1, t.ID, oneLine(t.Description, 80))
	}
	for _, n := range pickNumbers(w, ask("  Install which? Numbers, comma-separated (Enter for none):"), len(offer)) {
		self("templates", "install", offer[n].ID)
	}
	return nil
}

// pickNumbers reads "1, 3" as indexes into a list of n (0-based), skipping
// what isn't one of its numbers.
func pickNumbers(w io.Writer, answer string, n int) []int {
	var out []int
	for _, f := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		i, err := strconv.Atoi(f)
		if err != nil || i < 1 || i > n {
			fmt.Fprintf(w, "  %q isn't one of the numbers; skipped\n", f)
			continue
		}
		out = append(out, i-1)
	}
	return out
}

// runSelf runs a procauto command (this binary, this config) attached to
// the terminal, so its own questions and sign-in flows work as usual. It
// reports its own errors there; a failure to start it is written here.
func runSelf(a *engine.App, in io.Reader, out, errOut io.Writer, args ...string) {
	exe, err := os.Executable()
	if err == nil {
		c := exec.Command(exe, append([]string{"--config", a.Layout.Config()}, args...)...)
		c.Stdin, c.Stdout, c.Stderr = in, out, errOut
		err = c.Run()
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		fmt.Fprintln(errOut, "  ", err)
	}
}
