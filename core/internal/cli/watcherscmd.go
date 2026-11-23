package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Watchers are programs the daemon runs to hear about events, for hooks. As
// a program is, a watcher is added and trusted only by me (neither is an MCP
// tool), and what runs is the app's copy.

// WatcherInfo is one row of `watchers list --json`.
type WatcherInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// Mode is poll (run every interval) or stream (one long-lived process).
	Mode string `json:"mode" jsonschema:"enum=poll,enum=stream"`
	// Interval and Timeout are a poll watcher's: how often it runs, and how
	// long one poll may take.
	Interval string `json:"interval,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	// Executable is the app's copy, the one that runs; Checksum its SHA-256
	// (trust is keyed to it).
	Executable string          `json:"executable"`
	Checksum   string          `json:"checksum,omitempty"`
	Args       []string        `json:"args"`
	Secrets    []ProgramSecret `json:"secrets"`
	// Enabled is false while it's paused.
	Enabled   bool   `json:"enabled"`
	Trusted   bool   `json:"trusted"`
	Untrusted string `json:"untrusted,omitempty"`
	// Hooks are the hooks that listen to it; it runs while one of them is
	// on (and it's trusted and not paused).
	Hooks []string `json:"hooks"`
	// LastPollAt is when a poll last worked; StartedAt when its stream last
	// started; LastEventAt when it last printed a new event.
	LastPollAt  *time.Time `json:"lastPollAt,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	LastEventAt *time.Time `json:"lastEventAt,omitempty"`
	// Error is why its last poll failed or its stream last ended early
	// (cleared once it works again, or a permission it needs changes), and
	// ErrorAt when.
	Error   string     `json:"error,omitempty"`
	ErrorAt *time.Time `json:"errorAt,omitempty"`
	// Permissions are what the OS must allow it, as its file declares, with
	// what the scheduler last found: it isn't started while one isn't
	// granted. Absent when none.
	Permissions []engine.PermissionNeed `json:"permissions,omitempty"`
	// Events are the events it declares it prints, in its file's order,
	// each with its label and the hooks that act on it. Absent when it
	// declares none: it may print any type (Hint says so).
	Events []engine.WatcherEvent `json:"events,omitempty"`
	// Hint is a nudge about how it's written that doesn't keep it from
	// running (it doesn't declare its events): never a problem.
	Hint string `json:"hint,omitempty"`
}

// WatcherDetail is `watchers show --json`: a watcher with its description
// file's source.
type WatcherDetail struct {
	WatcherInfo
	Source string `json:"source"`
}

// WatcherChange is the output of `watchers add/update/delete/pause/resume
// --json`.
type WatcherChange struct {
	Watcher string `json:"watcher"`
	Path    string `json:"path"`
	// Enabled is whether it may run after the change (absent for deletes).
	Enabled *bool `json:"enabled,omitempty"`
}

// WatcherTrustOutput is `watchers trust --json`.
type WatcherTrustOutput struct {
	Watcher string `json:"watcher"`
	// Program is what trusting it approves: the executable that runs (by
	// checksum, with its description file), its arguments and secrets.
	Program ProgramInfo `json:"program"`
	Mode    string      `json:"mode" jsonschema:"enum=poll,enum=stream"`
	// Events are the events it declares it prints, which trusting it
	// covers (absent: it declares none).
	Events []engine.WatcherEvent `json:"events,omitempty"`
	// Changes since it was last trusted (empty for a first trust).
	Changes []string `json:"changes"`
	Trusted bool     `json:"trusted"`
	// Permissions are what the OS must allow it, which trusting it covers,
	// with what the scheduler last found (absent when none).
	Permissions []engine.PermissionNeed `json:"permissions,omitempty"`
}

func newWatchersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watchers",
		Short: "Watchers: programs the daemon runs to hear about events, for hooks to act on",
		Long: "A watcher is polled (run every interval, printing what's new since its cursor) or streams (one long-lived " +
			"process printing events as they happen); either way it prints JSON lines. It runs while a hook that's on " +
			"listens to it. How to write one: `" + product.Command + " guide hooks`.",
	}
	cmd.AddCommand(newWatchersListCmd(), newWatchersShowCmd(), newWatchersAddCmd(false), newWatchersAddCmd(true),
		newWatchersDeleteCmd(), newWatchersTrustCmd(), newWatchersPauseCmd(false), newWatchersPauseCmd(true))
	return cmd
}

// watcherInfo is a watcher with its trust, its state and the hooks that
// listen to it (all, when they're loaded already).
func watcherInfo(a *engine.App, w watchers.Watcher, all []engine.Runnable) WatcherInfo {
	info := WatcherInfo{
		Name: w.Name, Path: w.Path, Description: w.Description, Mode: w.Mode, Executable: w.Program.Executable,
		Args: w.Program.Args, Secrets: []ProgramSecret{}, Hooks: []string{}, Permissions: a.WatcherPermissions(w),
	}
	if info.Args == nil {
		info.Args = []string{}
	}
	if w.Mode == watchers.ModePoll {
		info.Interval, info.Timeout = settings.DurationText(w.Interval), settings.DurationText(w.Timeout)
	}
	info.Checksum, _ = programs.Checksum(w.Program.Executable)
	for _, s := range w.Program.Secrets {
		_, stored, _ := a.Stored(engine.Secret{Key: engine.WatcherSecretKey(w.Name, s.Name)})
		info.Secrets = append(info.Secrets, ProgramSecret{Name: s.Name, Env: s.Env, Stored: stored})
	}
	info.Enabled, _ = a.WatcherEnabled(w)
	if why, err := a.WatcherUntrusted(w); err == nil {
		info.Trusted, info.Untrusted = why == "", why
	}
	if all == nil {
		all, _, _ = a.Hooks()
	}
	for _, x := range all {
		if h, ok := engine.AsHook(x); ok && h.On.Watcher == w.Name {
			info.Hooks = append(info.Hooks, h.Name)
		}
	}
	if w.Declared() {
		info.Events = engine.WatcherEvents(w, all)
	}
	info.Hint = engine.WatcherHint(w)
	if st, err := watchers.LoadState(a.Layout.WatcherState()); err == nil {
		if ws := st.Watchers[w.Name]; ws != nil {
			info.LastPollAt, info.StartedAt, info.LastEventAt = ws.LastPollAt, ws.StartedAt, ws.LastEventAt
			info.Error, info.ErrorAt = ws.Error, ws.ErrorAt
		}
	}
	return info
}

func newWatchersListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List watchers: their mode, trust, the hooks listening and how they last went",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			ws, broken, err := a.Watchers()
			if err != nil {
				return err
			}
			warnBroken(cmd, broken)
			all, _, _ := a.Hooks()
			rows := make([]WatcherInfo, 0, len(ws))
			for _, w := range ws {
				rows = append(rows, watcherInfo(a, w, all))
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No watchers. Add one with `%s watchers add <name> --from <folder>/%s` (`%s guide hooks`).\n",
					product.Command, watchers.DescriptionFile, product.Command)
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tMODE\tENABLED\tTRUSTED\tHOOKS\tERROR")
			for _, w := range rows {
				hooksText := strings.Join(w.Hooks, ", ")
				if hooksText == "" {
					hooksText = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\t%s\n", w.Name, w.Mode, w.Enabled, w.Trusted, hooksText, oneLine(w.Error, 50))
			}
			return tw.Flush()
		}),
	}
	jsonFlag(cmd.Flags(), []WatcherInfo{})
	return cmd
}

func newWatchersShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "show <watcher>",
		Short:       "Show a watcher: its description file, trust, the hooks listening and how it last went",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			w, err := a.FindWatcher(args[0])
			if err != nil {
				return err
			}
			d := WatcherDetail{WatcherInfo: watcherInfo(a, w, nil), Source: w.Source}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), d)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)\n  mode      %s\n  enabled   %v\n  trusted   %v\n  hooks     %s\n", d.Name, d.Path, d.Mode,
				d.Enabled, d.Trusted, strings.Join(d.Hooks, ", "))
			for i, e := range d.Events {
				label := "  events    "
				if i > 0 {
					label = "            "
				}
				heard := "no hook"
				if len(e.Hooks) > 0 {
					heard = strings.Join(e.Hooks, ", ")
				}
				fmt.Fprintf(out, "%s%s: %s (%s)\n", label, e.Type, e.Label, heard)
			}
			if d.Error != "" {
				fmt.Fprintf(out, "  error     %s\n", d.Error)
			}
			if d.Hint != "" {
				fmt.Fprintf(out, "  hint      %s\n", d.Hint)
			}
			fmt.Fprintf(out, "\n%s", d.Source)
			return nil
		}),
	}
	jsonFlag(cmd.Flags(), WatcherDetail{})
	return cmd
}

func newWatchersAddCmd(update bool) *cobra.Command {
	var from string
	use, short := "add <watcher>", "Add a watcher from its watcher.yaml (its executable beside it); it runs once trusted"
	if update {
		use, short = "update <watcher>", "Replace a watcher's copy from its watcher.yaml; a changed one needs trusting again"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: "--from is the watcher's description file, watcher.yaml: it and the executable it names are copied into the " +
			"app's watchers folder, and that copy is what runs. It runs only once trusted (`" + product.Command +
			" watchers trust <watcher>`), and only while a hook that's on listens to it. Only you add a watcher, from a terminal.",
		Example: "  " + product.Command + " watchers add calendar --from ./calendar/watcher.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			name := args[0]
			if from == "" || from == "-" {
				return errors.New("a watcher is added from its description file's path (--from <path>/" + watchers.DescriptionFile + "), in a terminal")
			}
			raw, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			parsed, err := watchers.Parse(name, string(raw))
			if err != nil {
				return err
			}
			if len(parsed.Program.Secrets) > 0 {
				if s, ok := a.FindSignIn(name); ok && s.Kind != "watcher" {
					return fmt.Errorf("%s is already a sign-in's name (`%s auth login %s`): give a watcher with secrets another name", name, product.Command, name)
				}
			}
			if !update {
				if why := a.NameTaken(name); why != "" {
					return errors.New(why)
				}
			}
			w, err := watchers.Add(a.Layout.Watchers(), name, from, update)
			if err != nil {
				return err
			}
			enabled, _ := a.WatcherEnabled(w)
			out := WatcherChange{Watcher: name, Path: w.Path, Enabled: &enabled}
			hint := ""
			if h := engine.WatcherHint(w); h != "" {
				hint = "\nHint: " + h + "."
			}
			if !update {
				return emit(cmd, out, fmt.Sprintf("Added the watcher %s (%s), not trusted: it runs only once you trust it.\n"+
					"Trusting a hook that listens to it trusts it too (%s hooks trust <hook> --resume), or: %s watchers trust %s%s",
					name, w.Path, product.Command, product.Command, name, hint))
			}
			text := fmt.Sprintf("Updated the watcher %s (%s).", name, w.Path)
			if why, err := a.WatcherUntrusted(w); err == nil && why != "" {
				text += " It isn't trusted: " + why
			}
			// A hook listening for a type it no longer declares hears nothing.
			if all, _, err := a.Hooks(); err == nil {
				for _, x := range all {
					if h, ok := engine.AsHook(x); ok && h.On.Watcher == name {
						if err := a.CheckOn(h.On); err != nil {
							text += fmt.Sprintf("\nThe hook %s hears nothing: %v.", h.Name, err)
						}
					}
				}
			}
			return emit(cmd, out, text+hint)
		}),
	}
	cmd.Flags().StringVar(&from, "from", "", "the watcher's description file, with its executable beside it")
	hideFromMCP(cmd, "from")
	jsonFlag(cmd.Flags(), WatcherChange{})
	return cmd
}

func newWatchersDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <watcher>",
		Short: "Delete a watcher: its copy, settings, trust, secrets and state (the hooks listening to it stay)",
		Args:  cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			name := args[0]
			var keys []string
			if s, ok := a.FindSignIn(name); ok && s.Kind == "watcher" {
				for _, sec := range s.Secrets {
					keys = append(keys, sec.Key)
				}
			}
			path, err := watchers.Delete(a.Layout.Watchers(), name)
			if err != nil {
				return err
			}
			if err := (settings.Store{Path: a.Layout.Config()}).ResetPrefix("watchers." + name + "."); err != nil {
				return fmt.Errorf("deleted %s, but its settings remain: %w", path, err)
			}
			if err := a.TrustStore().Forget(name); err != nil {
				return fmt.Errorf("deleted %s, but not its trust: %w", path, err)
			}
			for _, k := range keys {
				if err := a.Connector.Secrets.Delete(k); err != nil && !errors.Is(err, secrets.ErrNotFound) {
					return fmt.Errorf("deleted %s, but not its secrets: %w", path, err)
				}
			}
			return emit(cmd, WatcherChange{Watcher: name, Path: path}, fmt.Sprintf("Deleted the watcher %s.", name))
		}),
	}
	jsonFlag(cmd.Flags(), WatcherChange{})
	return cmd
}

func newWatchersTrustCmd() *cobra.Command {
	var yes, check bool
	cmd := &cobra.Command{
		Use:   "trust <watcher>",
		Short: "Review a watcher's executable and secrets, and trust it to run",
		Long: "Shows the executable that runs (its checksum), its arguments and secrets, and what changed since you last " +
			"trusted it, then asks. Another executable or a changed description file takes the trust away. The hooks that " +
			"listen to it need trusting of their own.",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			w, err := a.FindWatcher(args[0])
			if err != nil {
				return err
			}
			approval, err := a.WatcherApproval(w)
			if err != nil {
				return err
			}
			info := watcherInfo(a, w, nil)
			out := WatcherTrustOutput{Watcher: w.Name, Mode: w.Mode, Changes: []string{}, Events: info.Events,
				Program:     ProgramInfo{Executable: info.Executable, Checksum: info.Checksum, Args: info.Args, Secrets: info.Secrets},
				Permissions: info.Permissions}
			f, _, err := a.TrustStore().Load()
			if err != nil {
				return err
			}
			if rec, ok := f.Routines[w.Name]; ok {
				out.Changes = append(out.Changes, rec.ChangesTo(approval)...)
				out.Trusted = rec.Approves(approval)
			}
			if out.Trusted && !check {
				return emit(cmd, out, w.Name+" is already trusted as it is now.")
			}
			if !wantsJSON(cmd) {
				describeWatcher(cmd, out)
				describePermissions(cmd.OutOrStdout(), out.Permissions)
			}
			if check {
				return emit(cmd, out, "\nNothing trusted (--check only reviews).")
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return errors.New("trusting is your decision: run it in a terminal, or add --yes after reviewing the above")
				}
				if !newPrompt(cmd).Confirm("\nTrust "+w.Name+" to run?", false) {
					return errors.New("nothing changed")
				}
			}
			if err := a.TrustStore().Trust(map[string]trust.Approval{w.Name: approval}, time.Now()); err != nil {
				return err
			}
			out.Trusted = true
			return emit(cmd, out, "Trusted "+w.Name+".")
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask (after reviewing it)")
	cmd.Flags().BoolVar(&check, "check", false, "only show what it runs and what changed; trust nothing")
	cmd.MarkFlagsMutuallyExclusive("check", "yes")
	// --check only reviews: it changes nothing, so it doesn't wake the scheduler.
	cmd.Annotations = map[string]string{annReadOnlyWith: "check"}
	jsonFlag(cmd.Flags(), WatcherTrustOutput{})
	return cmd
}

// describeWatcher prints what trusting a watcher approves.
func describeWatcher(cmd *cobra.Command, out WatcherTrustOutput) {
	w := cmd.OutOrStdout()
	p := out.Program
	fmt.Fprintf(w, "%s is a %s watcher; trusting it lets it run:\n", out.Watcher, out.Mode)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  executable\t%s\n", p.Executable)
	fmt.Fprintf(tw, "  sha-256\t%s\n", p.Checksum)
	if len(p.Args) > 0 {
		fmt.Fprintf(tw, "  arguments\t%s\n", strings.Join(p.Args, " "))
	}
	for _, s := range p.Secrets {
		fmt.Fprintf(tw, "  secret\t%s (as $%s)\n", s.Name, s.Env)
	}
	for _, e := range out.Events {
		fmt.Fprintf(tw, "  event\t%s: %s\n", e.Type, e.Label)
	}
	_ = tw.Flush()
	if len(out.Changes) > 0 {
		fmt.Fprintf(w, "\nSince you last trusted it, it %s.\n", strings.Join(out.Changes, "; "))
	}
}

func newWatchersPauseCmd(resume bool) *cobra.Command {
	use, short, verb := "pause <watcher>", "Stop a watcher: its process ends, and it isn't run until resumed", "Paused"
	if resume {
		use, short, verb = "resume <watcher>", "Let a paused watcher run again", "Resumed"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: "Pausing a watcher stops it (the daemon ends its process within about a second) and keeps it " +
			"stopped: the hooks listening to it hear nothing until it's resumed. Its cursor and the events it has seen are kept.",
		Args: cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			w, err := a.FindWatcher(args[0])
			if err != nil {
				return err
			}
			st := settings.Store{Path: a.Layout.Config(), Extra: settings.WatcherSettings(w.Name)}
			if _, err := st.Set("watchers."+w.Name+".enabled", fmt.Sprint(resume)); err != nil {
				return err
			}
			return emit(cmd, WatcherChange{Watcher: w.Name, Path: w.Path, Enabled: &resume}, fmt.Sprintf("%s %s.", verb, w.Name))
		}),
	}
	jsonFlag(cmd.Flags(), WatcherChange{})
	return cmd
}
