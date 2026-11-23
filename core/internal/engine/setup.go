package engine

import (
	"fmt"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/claudemcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/schedule"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// SetupStep is one thing first-run setup covers.
type SetupStep struct {
	// Step is what it sets up.
	Step string `json:"step" jsonschema:"enum=templates,enum=backend,enum=routine,enum=sign-in,enum=turn-on,enum=usage,enum=mcp"`
	// Subject is the server or routine it's about (routine, sign-in, turn-on).
	Subject string `json:"subject,omitempty"`
	// Hook is true when Subject is a hook (a turn-on step): turning it on
	// trusts it, and its watcher when that isn't trusted (`hooks trust`).
	Hook bool `json:"hook,omitempty"`
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
	// --browser`): a server, or the API key the claude-api backend needs.
	SignIn string `json:"signIn,omitempty"`
	// Setting is the setting the step chooses, for frontends to show and set
	// (backend: llm.backend).
	Setting string `json:"setting,omitempty"`
	// Settings are further settings the step needs set, for frontends to show
	// and set with each setting's own control (backend: the local backend's
	// server and model).
	Settings []string `json:"settings,omitempty"`
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
	StepTemplates = "templates"
	StepBackend   = "backend"
	StepRoutine   = "routine"
	StepSignIn    = "sign-in"
	StepTurnOn    = "turn-on"
	StepUsage     = "usage"
	StepMCP       = "mcp"
)

// SetupLeft is the setup steps still to do (not optional), for run status.
func (a *App) SetupLeft() ([]string, error) {
	out, err := a.Setup()
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

// Setup is where setup stands: `setup --json`. It only reads.
func (a *App) Setup() (SetupOutput, error) {
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return SetupOutput{}, err
	}
	backend, err := a.backendStep()
	if err != nil {
		return SetupOutput{}, err
	}
	steps := []SetupStep{templatesStep(set, a.templatesOffered()), backend}
	if st, ok, err := a.usageStep(); err != nil {
		return SetupOutput{}, err
	} else if ok {
		steps = append(steps, st)
	}
	if st, ok, err := a.MCPStep(); err != nil {
		return SetupOutput{}, err
	} else if ok {
		steps = append(steps, st)
	}
	loaded, problems, err := a.loadRoutines()
	if err != nil {
		return SetupOutput{}, err
	}
	steps = append(steps, problems...)
	steps = append(steps, a.signInSteps(loaded)...)
	turnOns, err := a.turnOnSteps(loaded)
	if err != nil {
		return SetupOutput{}, err
	}
	steps = append(steps, turnOns...)
	steps = append(steps, a.hookTurnOnSteps()...)

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
	st := SetupStep{Step: StepTemplates, Title: "Templates", Done: len(set.Routines) > 0, Optional: true, Status: "No routines installed yet",
		Ask: "First, on its own: which of the templates' projects do you work on (name each one, even if there's " +
			"only one)? Then, each template as its own choice: which should be installed as routines, of those of " +
			"your projects and those for any project? Templates with the same name are alternatives. A hook comes " +
			"with the watcher it listens to (withWatcher): it's one choice, installed together. None are picked by default.",
		Commands: []string{
			product.Command + " templates list --json   # each template's project (none: for any project); offer only the ones of the projects they work on, and those with none, and only those with supportsBackend (the others need another llm.backend); a hook with withWatcher is one choice with its watcher, its params theirs together",
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

// templatesOffered counts the templates that can be installed (for this OS
// and the selected backend, not installed yet); a build that doesn't know
// where its templates are has none.
func (a *App) templatesOffered() int {
	rows, err := a.Templates(false)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range rows {
		if t.Installable() {
			n++
		}
	}
	return n
}

func (a *App) backendStep() (SetupStep, error) {
	b, err := a.BackendReady()
	if err != nil {
		return SetupStep{}, err
	}
	def, _ := settings.Lookup("llm.backend")
	backend, err := a.BackendDef()
	if err != nil {
		return SetupStep{}, err
	}
	st := SetupStep{Step: StepBackend, Title: "Model backend", Done: b.Problem == "", Status: b.Status(), SignIn: b.SignIn,
		Setting: def.Key, Settings: backend.Settings,
		Ask:      "Which backend should run your routines?\n" + strings.Join(ChoiceLines(def, "- ", ": "), "\n"),
		Commands: []string{product.Command + " settings set " + def.Key + " <" + strings.Join(def.Enum, "|") + ">"}}
	if b.SignIn != "" {
		st.Commands = append(st.Commands, product.Command+" auth login "+b.SignIn+" --browser")
	}
	// The local backend's server and model (claude-api runs local/<model>
	// routines on that server too).
	url, _ := settings.Lookup(LocalURLSetting)
	vals, err := a.Settings()
	if err != nil {
		return SetupStep{}, err
	}
	now := vals.String(LocalURLSetting)
	if b.Name == agent.LocalBackend && b.Problem != "" {
		st.Ask += fmt.Sprintf("\nOn local: which model should routines run on (its name on your server, e.g. qwen3:8b; "+
			"`ollama list` shows the ones pulled)? And is the server at %s%s, or where is it?", now, OllamaNote(now, url.Default))
	}
	st.Commands = append(st.Commands,
		fmt.Sprintf("%s settings set %s <url>   # local only, experimental and untested: when the server isn't at %s%s",
			product.Command, LocalURLSetting, now, OllamaNote(now, url.Default)),
		fmt.Sprintf("%s settings set %s <model>   # local only: the model routines run on, its name on the server (e.g. qwen3:8b)",
			product.Command, LocalModelSetting),
		product.Command+" auth login local --browser   # local only: when the server needs an API key (doctor says so; hosted ones do, Ollama doesn't)")
	return st, nil
}

// OllamaNote names Ollama on this machine when url is the default one.
func OllamaNote(url string, def any) string {
	if url == fmt.Sprint(def) {
		return " (Ollama on this machine)"
	}
	return ""
}

// usageStep is there while the app can't read the Claude plan's usage from
// Claude Code's /usage (claude-code only; usage.Plan.CantRead: not for one
// failed try): it reads it from Claude Code's status line instead, once that
// runs the app's command.
func (a *App) usageStep() (SetupStep, bool, error) {
	def, err := a.BackendDef()
	if err != nil || def.PlanUsage == nil {
		return SetupStep{}, false, err
	}
	plan, err := usage.LoadPlan(a.Layout.PlanUsage())
	if err != nil || !plan.CantRead() {
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
	st := SetupStep{Step: StepUsage, Title: "Plan usage", Done: installed,
		Status: "Read from your Claude Code status line (Claude Code's /usage can't be read)",
		Ask: "The app can't read your Claude plan's usage from Claude Code right now. Read it from your Claude Code " +
			"status line instead? Your status line keeps showing what it shows now.",
		Commands: []string{product.Command + " usage statusline install"}}
	if !installed {
		st.Status = "Can't be read from Claude Code's /usage: " + plan.TryError
	}
	return st, true, nil
}

// MCPStep is there on the claude-code backend: Claude Code gets the app's
// MCP server at user scope, so I can manage the app from any Claude Code
// session. Required, and done without asking (it only adds a server to
// Claude Code, which asks before each tool call that changes anything).
func (a *App) MCPStep() (SetupStep, bool, error) {
	def, err := a.BackendDef()
	if err != nil || def.Name != "claude-code" {
		return SetupStep{}, false, err
	}
	problem, err := claudemcp.Problem(product.Command)
	if err != nil {
		return SetupStep{}, false, err
	}
	st := SetupStep{Step: StepMCP, Title: "Claude Code connection", Done: problem == "", Auto: true,
		Status:   "Claude Code has " + product.Command + "'s MCP server: /mcp lists it in every folder",
		Commands: []string{product.Command + " mcp connect"}}
	if problem != "" {
		st.Status = Capitalize(problem)
	}
	return st, true, nil
}

// loadedRoutine is a routine of either kind whose settings resolve.
type loadedRoutine struct {
	Runnable
	cfg RunConfig
}

// loadRoutines resolves each routine's settings, of either kind; a routine
// that doesn't load, settings that don't resolve, or what keeps it from
// running whether trusted or not (a missing folder or server, a permission
// that isn't granted) is a step to fix (it would fail every run).
func (a *App) loadRoutines() ([]loadedRoutine, []SetupStep, error) {
	all, broken, err := a.All()
	if err != nil {
		return nil, nil, err
	}
	var loaded []loadedRoutine
	var problems []SetupStep
	fix := func(name, problem string, commands ...string) {
		if len(commands) == 0 {
			commands = []string{product.Command + " doctor   # says what's wrong and how to fix it"}
		}
		problems = append(problems, SetupStep{Step: StepRoutine, Subject: name, Title: "Fix " + name,
			Status: Capitalize(problem), Commands: commands})
	}
	for _, b := range broken {
		fix(b.Name, b.Err.Error())
	}
	for _, x := range all {
		rd := a.Assess(x)
		if rd.ConfigErr != nil {
			fix(x.Name(), rd.ConfigErr.Error())
			continue
		}
		for _, p := range rd.Problems {
			if p.Permission != "" && p.Open != "" {
				// The OS asks me, as the app; or I turn it on where it says.
				fix(x.Name(), p.Text, product.Command+" permissions request "+p.Permission)
				continue
			}
			fix(x.Name(), p.Text)
		}
		loaded = append(loaded, loadedRoutine{x, rd.Config})
	}
	return loaded, problems, nil
}

// signInSteps is one step per sign-in the routines need (the servers they
// reach, through plugins too; a program's own secrets), done or not.
func (a *App) signInSteps(loaded []loadedRoutine) []SetupStep {
	users := map[string][]string{} // sign-in -> routines needing it
	for _, r := range loaded {
		for _, s := range r.reaches(a) {
			users[s] = append(users[s], r.Name())
		}
	}
	var steps []SetupStep
	for _, s := range a.SignIns() { // sorted by name
		who := users[s.Name]
		if len(who) == 0 {
			continue
		}
		in := a.State(s).SignedIn
		title := "Sign in to " + s.Name
		if s.AccountOf != "" {
			title += " (an account of " + s.AccountOf + ")"
		}
		st := SetupStep{Step: StepSignIn, Subject: s.Name, Title: title, Done: in,
			Status:   "Signed in; used by " + strings.Join(who, ", "),
			Ask:      "Sign in to " + s.Name + "? It opens the browser.",
			Commands: []string{product.Command + " auth login " + s.Name + " --browser"}}
		if !in {
			st.Status, st.SignIn, st.Prepare = "Not signed in; used by "+strings.Join(who, ", "), s.Name, s.Prepare
		}
		steps = append(steps, st)
	}
	return steps
}

// turnOnSteps is a step per routine: trusting what it can reach and, when
// it has a schedule, running on it, in one yes (`routines trust --resume`).
// It's done once the routine is trusted and isn't paused. Trusting is
// required; resuming a trusted routine is optional once another routine runs
// on its schedule (a paused one may be paused on purpose).
func (a *App) turnOnSteps(loaded []loadedRoutine) ([]SetupStep, error) {
	untrusted := map[string]string{}
	for _, r := range loaded {
		why, _, err := a.TrustProblem(r)
		if err != nil {
			return nil, err
		}
		if why != "" {
			untrusted[r.Name()] = why
		}
	}
	steps := make([]SetupStep, 0, len(loaded))
	for _, r := range loaded {
		name := r.Name()
		why, words := untrusted[name], schedule.Text(r.cfg.Schedule)
		paused := r.cfg.Schedule != "" && !r.cfg.Enabled
		st := SetupStep{Step: StepTurnOn, Subject: name, Title: "Turn on " + name, Done: why == "" && !paused,
			Commands: []string{product.Command + " routines trust " + name + " --yes --resume"}}
		switch {
		case why != "":
			st.Status = Capitalize(why)
			st.Ask = "Trust " + name + " to run for real with what it can reach"
			switch {
			case paused:
				st.Status += "; paused (" + words + ")"
				st.Ask += ", and run it on its schedule (" + words + ")"
			case r.cfg.Schedule != "":
				st.Status += ", so it can't run on its schedule (" + words + ")"
			}
			st.Ask += "?"
			st.Commands = append([]string{product.Command + " routines trust " + name + " --check --json   # show me what it can reach before asking"}, st.Commands...)
		case paused:
			// Trusted, it was turned on once: paused is my choice, never
			// unfinished setup (nor red), so resuming it is only offered.
			st.Optional = true
			st.Status = "Trusted, but paused: it doesn't run on its schedule (" + words + ")"
			st.Ask = "Run " + name + " on its schedule (" + words + ")?"
		case r.cfg.Schedule != "":
			st.Status = "Trusted; runs on its schedule: " + words
		default:
			st.Status = "Trusted; it has no schedule, so it runs when started"
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// hookTurnOnSteps is a step per hook that's on but can't run only because
// it, or its watcher, isn't trusted (never, or not since it changed):
// turning it on trusts both, in one yes (`hooks trust --resume`). A hook
// that's off is off on purpose: no step.
func (a *App) hookTurnOnSteps() []SetupStep {
	all, _, err := a.Hooks()
	if err != nil {
		return nil
	}
	var steps []SetupStep
	for _, x := range all {
		rd := a.Assess(x)
		if rd.ConfigErr != nil || !rd.Config.Enabled || !OnlyTrust(rd.All()) {
			continue
		}
		name := x.Name()
		var why []string
		for _, p := range rd.All() {
			why = append(why, p.Text)
		}
		steps = append(steps, SetupStep{Step: StepTurnOn, Subject: name, Hook: true, Title: "Turn on " + name,
			Status: Capitalize(strings.Join(why, "; ")),
			Ask:    "Trust " + name + " (and its watcher, if it isn't trusted) to run when its events come?",
			Commands: []string{
				product.Command + " hooks trust " + name + " --check --json   # show me what it acts on and can reach before asking",
				product.Command + " hooks trust " + name + " --yes --resume",
			}})
	}
	return steps
}

// ChoiceLines are an enum setting's values, each with its help, aligned:
// "<prefix><value><sep><help>" ("- claude-code: …", or "claude-code  …").
func ChoiceLines(def settings.Setting, prefix, sep string) []string {
	width := 0
	for _, v := range def.Enum {
		width = max(width, len(v))
	}
	lines := make([]string, 0, len(def.Enum))
	for _, v := range def.Enum {
		help := def.EnumHelp[v]
		switch {
		case help == "":
			lines = append(lines, prefix+v)
		case sep == "  ":
			lines = append(lines, fmt.Sprintf("%s%-*s%s%s", prefix, width, v, sep, help))
		default:
			lines = append(lines, prefix+v+sep+help)
		}
	}
	return lines
}

// Capitalize starts a status with a capital, as every status does.
func Capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
