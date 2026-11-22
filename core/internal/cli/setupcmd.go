package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
)

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided first run: templates, the model backend, sign-ins, and turning routines on",
		Long: "In a terminal, walks through each step and asks before doing anything. With --json it changes " +
			"nothing: it lists the steps, what's done, what to ask, and the commands that do each one, for an LLM " +
			"or a frontend guiding me. Run it again any time; done steps are skipped.",
		Annotations: map[string]string{annReadOnlyWith: "json", annNotReadOnly: "setting up the app is mine"},
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			if wantsJSON(cmd) {
				out, err := a.Setup()
				if err != nil {
					return err
				}
				return writeJSON(cmd.OutOrStdout(), out)
			}
			if !isTerminal(os.Stdin) {
				return errors.New("setup asks questions: run it in a terminal, or see the steps with --json")
			}
			return setupInteractive(cmd, a)
		}),
	}
	jsonFlag(cmd.Flags(), engine.SetupOutput{}).Usage = "list the steps and the commands for each, changing nothing"
	return cmd
}

// setupInteractive walks through the steps in a terminal, asking before each:
// it installs templates, signs in and turns routines on through the same
// commands setup --json lists (run attached to the terminal, so their own
// questions work), and sets the backend directly.
func setupInteractive(cmd *cobra.Command, a *engine.App) error {
	w := cmd.OutOrStdout()
	p := newPrompt(cmd)
	// A command that fails says why itself, on the terminal.
	self := func(args ...string) { runSelf(a, cmd.InOrStdin(), w, cmd.ErrOrStderr(), args...) }

	fmt.Fprintln(w, "Setting up "+product.Name+". Nothing changes without your yes.")

	fmt.Fprintln(w, "\n1. Templates")
	if err := setupTemplates(a, p, self); err != nil {
		fmt.Fprintln(w, "  Skipped:", err)
	}

	fmt.Fprintln(w, "\n2. Model backend")
	b, err := a.BackendReady()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "  Now: "+b.Status())
	def, _ := settings.Lookup("llm.backend")
	fmt.Fprintln(w, "  Which backend should run your routines?")
	for i, line := range engine.ChoiceLines(def, "", "  ") {
		fmt.Fprintf(w, "    %d. %s\n", i+1, line)
	}
	for {
		choice := strings.ToLower(p.Pick("  Number or name [Enter keeps "+b.Name+"]:", def.Enum))
		if choice == "" || choice == b.Name {
			break
		}
		if _, err := (settings.Store{Path: a.Layout.Config()}).Set("llm.backend", choice); err != nil {
			fmt.Fprintf(w, "  %q isn't one of them: a number from 1 to %d, or a name\n", choice, len(def.Enum))
			continue
		}
		break
	}
	if b, err = a.BackendReady(); err != nil {
		return err
	}
	if b.Name == agent.LocalBackend {
		if err := setupLocal(cmd.Context(), a, p, self); err != nil {
			return err
		}
		if b, err = a.BackendReady(); err != nil {
			return err
		}
	}
	if b.SignIn != "" && p.Confirm("  "+engine.Capitalize(b.Problem)+". Sign in to "+b.SignIn+" now?", true) {
		self("auth", "login", b.SignIn)
	} else if b.Problem != "" {
		fmt.Fprintln(w, "  Still needed: "+b.Problem)
	}
	if st, ok, err := a.MCPStep(); err != nil {
		return err
	} else if ok && !st.Done {
		fmt.Fprintln(w, "  Connecting Claude Code to "+product.Command+"'s MCP server:")
		self("mcp", "connect")
	}

	fmt.Fprintln(w, "\n3. Routines and sign-ins")
	steps, err := a.Setup()
	if err != nil {
		return err
	}
	for _, s := range steps.Steps {
		if s.Done {
			continue
		}
		switch s.Step {
		case engine.StepRoutine:
			fmt.Fprintf(w, "  %s: %s (`"+product.Command+" doctor` says how to fix it)\n", s.Title, s.Status)
		case engine.StepSignIn:
			if p.Confirm("  "+s.Status+". "+s.Ask, true) {
				self("auth", "login", s.Subject)
			}
		case engine.StepUsage:
			if p.Confirm("  "+s.Ask, true) {
				self("usage", "statusline", "install")
			}
		case engine.StepTurnOn:
			fmt.Fprintf(w, "  %s: %s\n", s.Title, s.Status)
			self("routines", "trust", s.Subject, "--resume") // shows what it can reach, then asks
		}
	}

	final, err := a.Setup()
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

// setupLocal asks for the local backend's server and model: the server's
// URL (Enter keeps it), then one of the models it lists, by number or name,
// or a model's name when it doesn't answer. It asks for the server's API key
// only when the server wants one (HTTP 401 or 403).
func setupLocal(ctx context.Context, a *engine.App, p *prompt, self func(...string)) error {
	w := p.out
	store := settings.Store{Path: a.Layout.Config()}
	vals, err := a.Settings()
	if err != nil {
		return err
	}
	def, _ := settings.Lookup(engine.LocalURLSetting)
	url := vals.String(engine.LocalURLSetting)
	for {
		answer := p.Ask("  Your model server's URL [Enter keeps " + url + engine.OllamaNote(url, def.Default) + "]:")
		if answer == "" {
			break
		}
		if (agent.OpenAICompat{BaseURL: answer}).CheckURL() != nil {
			fmt.Fprintf(w, "  %q isn't an http(s) URL, e.g. %v\n", answer, def.Default)
			continue
		}
		if _, err := store.Set(engine.LocalURLSetting, answer); err != nil {
			return err
		}
		break
	}
	models := func() ([]string, bool, error) {
		c, err := a.Local()
		if err != nil {
			return nil, false, err
		}
		return c.Models(ctx)
	}
	ids, listed, err := models()
	var needsKey *agent.NeedsKeyError
	q := "  The server at %s needs an API key. Enter it now?"
	if errors.As(err, &needsKey) && needsKey.Refused {
		q = "  The server at %s refuses its API key. Enter another now?"
	}
	if needsKey != nil && p.Confirm(fmt.Sprintf(q, needsKey.BaseURL), true) {
		self("auth", "login", agent.LocalProvider)
		ids, listed, err = models()
	}
	keep := ""
	if now := vals.String(engine.LocalModelSetting); now != "" {
		keep = " [Enter keeps " + now + "]"
	}
	byName := "  Which model should routines run on? Its name on the server (e.g. qwen3:8b)" + keep + ":"
	var model string
	switch {
	case err != nil:
		fmt.Fprintln(w, "  "+engine.Capitalize(err.Error())+".")
		model = p.Ask(byName)
	case listed && len(ids) > 0:
		fmt.Fprintln(w, "  Which model should routines run on? The server has:")
		for i, id := range ids {
			fmt.Fprintf(w, "    %d. %s\n", i+1, id)
		}
		model = p.Pick("  Number or name"+keep+":", ids)
	case listed:
		fmt.Fprintln(w, "  The server has no models yet (on Ollama, pull one in a terminal: `ollama pull qwen3:8b`).")
		model = p.Ask(byName)
	default: // it answers in a shape of its own
		model = p.Ask(byName)
	}
	if model != "" {
		if _, err := store.Set(engine.LocalModelSetting, model); err != nil {
			return err
		}
	}
	return nil
}

// setupTemplates offers the templates for this OS not installed yet; none
// are picked by default.
func setupTemplates(a *engine.App, p *prompt, self func(...string)) error {
	w := p.out
	rows, err := a.Templates(false)
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
		for _, n := range pickNumbers(w, p.Ask("  Which do you work on? Numbers, comma-separated (Enter for none):"), len(projects)) {
			mine[projects[n]] = true
		}
	}
	var offer []engine.TemplateInfo
	for _, t := range rows {
		if t.Installable() && (t.Project == "" || mine[t.Project]) {
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
	for _, n := range pickNumbers(w, p.Ask("  Install which? Numbers, comma-separated (Enter for none):"), len(offer)) {
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
