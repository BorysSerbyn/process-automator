package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/schedule"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
)

// A routine runs for real only while what it can reach is what I trusted
// (see package trust). Dry runs never need trust.

// TrustOutput is `routines trust --json`. For a program, its surface is
// empty (it reaches no servers or tools through the app) and its program
// section says what trusting it approves.
type TrustOutput struct {
	Routine string `json:"routine"`
	// Kind is program for a program; absent for an LLM routine (llm).
	Kind    string        `json:"kind,omitempty" jsonschema:"enum=llm,enum=program"`
	Surface trust.Surface `json:"surface"`
	// Program is what trusting a program approves: the executable that
	// runs (by checksum, with its description file), what it declares.
	Program *ProgramInfo `json:"program,omitempty"`
	// Tools are its allowlisted tools with the server offering each and
	// whether it's read-only (absent when its servers couldn't be reached).
	Tools []agent.CheckedTool `json:"tools,omitempty"`
	// ToolsError says why its tools couldn't be checked (e.g. a server needs
	// a sign-in).
	ToolsError string `json:"toolsError,omitempty"`
	// Changes since it was last trusted (empty for a first trust).
	Changes []string `json:"changes"`
	Trusted bool     `json:"trusted"`
	// Resumed is true when --resume turned its schedule on (it was paused),
	// or turned a hook on.
	Resumed bool `json:"resumed,omitempty"`
	// Hook is true for a hook: Kind is its action's, and Trigger is what it
	// acts on, which trusting it covers too.
	Hook    bool           `json:"hook,omitempty"`
	Trigger *trust.Trigger `json:"trigger,omitempty"`
	// Watcher is the hook's watcher: trusting the hook (--yes) trusts it
	// too when it isn't trusted, so its review is part of the hook's.
	Watcher *WatcherTrust `json:"watcher,omitempty"`
	// Warnings are what to weigh before trusting it: an LLM hook with
	// write tools acts on outside event data.
	Warnings []string `json:"warnings,omitempty"`
	// SignIns are the sign-ins it still needs before it works (`auth login
	// <name>`): its servers', its own secrets' (a program's, a hook's), and
	// its watcher's. Trusting doesn't wait for them.
	SignIns []string `json:"signIns,omitempty"`
	// Permissions are what the OS must allow it before it works: an LLM
	// routine's plugins', a program's own, a hook's action's and its
	// watcher's, each with what declares it and what the scheduler last
	// found (`permissions request <permission>` asks for one). Trusting
	// covers them (one added later needs a new review) and doesn't wait for
	// them; while one isn't granted it doesn't run.
	Permissions []engine.PermissionNeed `json:"permissions,omitempty"`
}

// WatcherTrust is a hook's watcher in the hook's review: whether it's
// trusted, and what trusting it approves.
type WatcherTrust struct {
	Name    string `json:"name"`
	Trusted bool   `json:"trusted"`
	// Untrusted says why not (no such watcher, not trusted yet, changed).
	Untrusted string `json:"untrusted,omitempty"`
	// Description, Mode and Program are what it is and runs (absent when
	// there's no such watcher): its executable by checksum, its arguments
	// and secrets, each stored or not.
	Description string       `json:"description,omitempty"`
	Mode        string       `json:"mode,omitempty" jsonschema:"enum=poll,enum=stream"`
	Program     *ProgramInfo `json:"program,omitempty"`
	// Changes since it was last trusted (empty for a first trust).
	Changes []string `json:"changes,omitempty"`

	review *engine.WatcherReview // the engine's, which trusting it records
}

func newRoutinesTrustCmd() *cobra.Command {
	var yes, check, resume bool
	cmd := &cobra.Command{
		Use:   "trust <routine>",
		Short: "Review what a routine can reach, and trust it to run for real (--resume: and turn it on)",
		Long: "Shows the routine's servers, its tools (which ones write), built-in tools, plugin settings and folder, " +
			"and what changed since you last trusted it, then asks. A routine runs for real only while what it can " +
			"reach is what you trusted; a change to it (a new server or tool, another folder, ...) takes the trust " +
			"away until you trust it again. Its prompt, schedule and other settings don't. Dry runs never need it. " +
			"A program shows the executable that runs (its checksum), whether it writes, its arguments and secrets; " +
			"another executable or a changed description file takes the trust away, and it runs only while trusted, " +
			"dry runs included. " +
			"With --resume it also lets the routine run on its schedule (as `routines resume` does), in the same " +
			"yes: turning a routine on is one step.",
		Example: "  " + product.Command + " routines trust digest --check --json   # review only\n" +
			"  " + product.Command + " routines trust digest --yes --resume    # trust it and run it on its schedule",
		Args: cobra.ExactArgs(1),
		RunE: withAppForEdit(func(cmd *cobra.Command, a *engine.App, args []string) error {
			x, err := a.FindAny(args[0])
			if err != nil {
				return err
			}
			return trustRunnable(cmd, a, x, yes, check, resume)
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask (after reviewing what it can reach)")
	cmd.Flags().BoolVar(&check, "check", false, "only show what it can reach and what changed; trust nothing (frontends review with it, then trust with --yes)")
	cmd.Flags().BoolVar(&resume, "resume", false, "also let it run on its schedule, if it's paused (turns it on in one step)")
	cmd.MarkFlagsMutuallyExclusive("check", "yes")
	cmd.MarkFlagsMutuallyExclusive("check", "resume")
	// --check only reviews: it changes nothing, so it doesn't wake the scheduler.
	cmd.Annotations = map[string]string{annReadOnlyWith: "check"}
	jsonFlag(cmd.Flags(), TrustOutput{})
	return cmd
}

// trustRunnable reviews and trusts a routine of either kind, or a hook (with
// --resume, also turning it on), as `routines trust` and `hooks trust` do.
func trustRunnable(cmd *cobra.Command, a *engine.App, x engine.Runnable, yes, check, resume bool) error {
	name := x.Name()
	c, err := a.RunConfig(x)
	if err != nil {
		return err
	}
	f, _, err := a.TrustStore().Load()
	if err != nil {
		return err
	}
	approval, err := a.Approval(x)
	if err != nil {
		return err
	}
	h, isHook := engine.AsHook(x)
	out := TrustOutput{Routine: name, Kind: x.Kind().Written(), Surface: trust.Surface{}.Normalized(), Changes: []string{}, Hook: isHook, Trigger: approval.Trigger}
	if isHook {
		out.Watcher = hookWatcher(a, h.On.Watcher)
	}
	// A hook's watcher that isn't trusted is trusted with it: one review,
	// one yes.
	trustWatcher := out.Watcher != nil && !out.Watcher.Trusted && out.Watcher.review.Approval != nil
	out.SignIns = a.SignInsNeeded(x)
	out.Permissions = a.Permissions(x)
	if approval.Surface != nil {
		out.Surface = approval.Surface.Normalized()
	}
	if rec, ok := f.Routines[name]; ok {
		out.Changes = append(out.Changes, rec.ChangesTo(approval)...)
		out.Trusted = rec.Approves(approval)
	}
	// Only a paused routine with a schedule, or a paused hook, has anything
	// to resume.
	resuming := resume && !c.Enabled && (c.Schedule != "" || isHook)
	turnOn := "run it on its schedule (" + schedule.Text(c.Schedule) + ")"
	if isHook {
		turnOn = "turn it on (it runs on " + h.On.String() + ")"
	}
	question := "Trust " + name + " to run for real with this"
	if trustWatcher {
		question = "Trust " + name + " and its watcher " + out.Watcher.Name + " to run for real with this"
	}
	if resuming {
		question += ", and " + turnOn
	}
	switch {
	case out.Trusted && !check && !resuming && !trustWatcher:
		return emit(cmd, out, name+" is already trusted with what it can reach now.")
	case out.Trusted && !check && !trustWatcher:
		// Nothing to review: only the schedule to turn on.
		if !wantsJSON(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), name+" is already trusted with what it can reach now.")
		}
		question = strings.ToUpper(turnOn[:1]) + turnOn[1:]
		question = strings.Replace(question, " it ", " "+name+" ", 1)
	default:
		// What each kind shows for review.
		var toolsErr error
		if r, _, err := engine.AsRoutine(x, ""); err == nil {
			rc, _ := a.RoutineConfig(r)
			out.Tools, toolsErr = agent.CheckTools(cmd.Context(), a.Connector, engine.RunOf(r, rc.WorkFolder, ""), r.Servers, r.Tools)
			if toolsErr != nil {
				out.ToolsError = toolsErr.Error()
			}
		}
		if p, err := engine.AsProgram(x); err == nil {
			out.Program = programInfo(a, p)
		}
		if isHook {
			toolsErr = hookReview(cmd, a, h, &out)
		}
		if !wantsJSON(cmd) {
			describeSurface(cmd, out, toolsErr)
			if len(out.SignIns) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "It needs signing in before it works: %s.\n", signInCommands(out.SignIns))
			}
			describePermissions(cmd.OutOrStdout(), out.Permissions)
		}
		if check {
			note := "\nNothing trusted (--check only reviews)."
			if out.Trusted {
				note = "\nIt's trusted with what it can reach now."
			}
			return emit(cmd, out, note)
		}
		question = "\n" + question
	}
	if !yes {
		if !isTerminal(os.Stdin) {
			return errors.New("trusting is your decision: run it in a terminal, or add --yes after reviewing the above")
		}
		if !newPrompt(cmd).Confirm(question+"?", false) {
			return errors.New("nothing changed")
		}
	}
	var done []string
	// What was shown is what's trusted: the hook, and its watcher with it.
	var mine *trust.Approval
	if !out.Trusted {
		mine = &approval
	}
	var watcher *engine.WatcherReview
	if trustWatcher {
		watcher = out.Watcher.review
	}
	if err := a.TrustWith(name, mine, h.On.Watcher, watcher); err != nil {
		return err
	}
	if !out.Trusted {
		out.Trusted = true
		done = append(done, "Trusted "+name)
	}
	if trustWatcher {
		out.Watcher.Trusted, out.Watcher.Untrusted = true, ""
		done = append(done, "Trusted its watcher "+out.Watcher.Name)
	}
	if resuming {
		st := settings.Store{Path: a.Layout.Config(), Extra: x.Settings()}
		if _, err := st.Set(enabledKey(x), "true"); err != nil {
			return err
		}
		out.Resumed = true
		if isHook {
			done = append(done, "Turned "+name+" on: it runs on "+h.On.String())
		} else {
			done = append(done, "Resumed "+name+": it runs on its schedule")
		}
	}
	if out.Watcher != nil && !out.Watcher.Trusted {
		done = append(done, "It hears nothing: its watcher "+out.Watcher.Name+" "+out.Watcher.Untrusted)
	}
	if len(out.SignIns) > 0 {
		done = append(done, "It still needs signing in: "+signInCommands(out.SignIns))
	}
	for _, p := range engine.PermissionProblems(name, out.Permissions) {
		done = append(done, "Before it runs, "+p.Text+" (fix: "+p.Fix+")")
	}
	return emit(cmd, out, strings.Join(done, ". ")+".")
}

// describePermissions prints the permissions something needs, with what
// declares each and what the scheduler last found.
func describePermissions(w io.Writer, needs []engine.PermissionNeed) {
	if len(needs) == 0 {
		return
	}
	fmt.Fprintln(w, "It needs these permissions from the OS (trusting it covers them; it doesn't run while one isn't granted):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, n := range needs {
		var by []string
		for _, s := range n.By {
			by = append(by, s.Kind+" "+s.Name)
		}
		line := fmt.Sprintf("  %s\t%s\t(%s)", n.Label, permissionStateText(n.State), strings.Join(by, ", "))
		if n.Blocks() {
			line += "\tfix: " + engine.PermissionFix(n)
		}
		fmt.Fprintln(tw, line)
	}
	_ = tw.Flush()
}

// permissionStateText is a permission's state in words.
func permissionStateText(s permissions.Status) string {
	switch s {
	case permissions.Granted:
		return "granted"
	case permissions.Missing:
		return "not allowed"
	case permissions.Unknown:
		return "can't be checked"
	case permissions.Unsupported:
		return "not checked on this OS"
	default:
		return "not checked yet (the scheduler checks it)"
	}
}

// enabledKey is the key of x's enabled setting: routines.<name>.enabled, or
// a hook's hooks.<name>.enabled.
func enabledKey(x engine.Runnable) string {
	for _, s := range x.Settings() {
		if strings.HasSuffix(s.Key, ".enabled") {
			return s.Key
		}
	}
	return "routines." + x.Name() + ".enabled"
}

// describeSurface prints what a routine can reach, writes first.
func describeSurface(cmd *cobra.Command, out TrustOutput, toolsErr error) {
	w := cmd.OutOrStdout()
	if out.Hook {
		describeHook(w, out)
	}
	if out.Program != nil {
		describeProgram(w, out)
		return
	}
	fmt.Fprintf(w, "%s can reach:\n", out.Routine)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	none := func(l []string) string {
		if len(l) == 0 {
			return "(none)"
		}
		return strings.Join(l, ", ")
	}
	fmt.Fprintf(tw, "  servers\t%s\n", none(out.Surface.Servers))
	if toolsErr != nil {
		fmt.Fprintf(tw, "  tools\t%s\t(couldn't check which write: %v)\n", none(out.Surface.Tools), toolsErr)
	} else {
		var writes, reads []string
		for _, t := range out.Tools {
			if t.ReadOnly {
				reads = append(reads, t.Name)
			} else {
				writes = append(writes, t.Name+" ("+t.Server+")")
			}
		}
		fmt.Fprintf(tw, "  writes\t%s\n", none(writes))
		fmt.Fprintf(tw, "  reads\t%s\n", none(reads))
	}
	if len(out.Surface.BuiltinTools) > 0 {
		fmt.Fprintf(tw, "  built-in tools\t%s\n", none(out.Surface.BuiltinTools))
	}
	folder := out.Surface.Folder
	if folder == "" {
		folder = "(an empty temporary folder)"
	}
	fmt.Fprintf(tw, "  folder\t%s\n", folder)
	for name, section := range out.Surface.Plugins {
		fmt.Fprintf(tw, "  %s plugin\t%v\n", name, section)
	}
	_ = tw.Flush()
	if len(out.Changes) > 0 {
		fmt.Fprintf(w, "\nSince you last trusted it, it %s.\n", strings.Join(out.Changes, "; "))
	}
}

// describeProgram prints what trusting a program approves.
func describeProgram(w io.Writer, out TrustOutput) {
	p := out.Program
	if out.Hook {
		fmt.Fprintln(w, "Trusting it lets it run:")
	} else {
		fmt.Fprintf(w, "%s is a program; trusting it lets it run:\n", out.Routine)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  executable\t%s\n", p.Executable)
	fmt.Fprintf(tw, "  sha-256\t%s\n", p.Checksum)
	fmt.Fprintf(tw, "  writes\t%v\n", p.Writes)
	if len(p.Args) > 0 {
		fmt.Fprintf(tw, "  arguments\t%s\n", strings.Join(p.Args, " "))
	}
	for _, s := range p.Secrets {
		fmt.Fprintf(tw, "  secret\t%s (as $%s)\n", s.Name, s.Env)
	}
	_ = tw.Flush()
	if len(out.Changes) > 0 {
		fmt.Fprintf(w, "\nSince you last trusted it, it %s.\n", strings.Join(out.Changes, "; "))
	}
}

// hookReview adds what trusting a hook covers besides its action: its
// trigger, its watcher's own trust, and the warning for an LLM hook that
// can write and acts on outside data. It returns why its tools couldn't be
// checked (an LLM hook's).
func hookReview(cmd *cobra.Command, a *engine.App, h hooks.Hook, out *TrustOutput) error {
	if h.Program != nil {
		out.Program = programInfo(a, *h.Program)
	}
	if h.Prompt == nil {
		return nil
	}
	r := *h.Prompt
	rc, _ := a.RoutineConfig(r)
	tools, err := agent.CheckTools(cmd.Context(), a.Connector, engine.RunOf(r, rc.WorkFolder, ""), r.Servers, r.Tools)
	out.Tools = tools
	if err != nil {
		out.ToolsError = err.Error()
	}
	var writes []string
	for _, t := range tools {
		if !t.ReadOnly {
			writes = append(writes, t.Name)
		}
	}
	if err != nil {
		writes = append(writes, r.Tools...) // which write is unknown: assume they may
	}
	writes = append(writes, a.WriteBuiltins(rc.BuiltinTools)...)
	if len(writes) > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("it can write (%s) and acts on outside event data: an event "+
			"from %s could try to steer what it writes. Its prompt gets the data marked untrusted, but if it needn't "+
			"write, read-only tools are safer; or let a program act on it", strings.Join(writes, ", "), h.On.Watcher))
	}
	return err
}

// hookWatcher is a hook's watcher as the hook's review shows it (the
// engine's review, with what it runs as `watchers show` puts it).
func hookWatcher(a *engine.App, name string) *WatcherTrust {
	r := a.HookWatcherReview(name)
	wt := &WatcherTrust{Name: name, Trusted: r.Trusted, Untrusted: r.Untrusted, Changes: r.Changes, review: &r}
	if r.Watcher != nil {
		info := watcherInfo(a, *r.Watcher, []engine.Runnable{})
		wt.Description, wt.Mode = info.Description, info.Mode
		wt.Program = &ProgramInfo{Executable: info.Executable, Checksum: info.Checksum, Args: info.Args, Secrets: info.Secrets}
	}
	return wt
}

// signInCommands are the commands that sign in to names.
func signInCommands(names []string) string {
	var cmds []string
	for _, n := range names {
		cmds = append(cmds, "`"+product.Command+" auth login "+n+"`")
	}
	return strings.Join(cmds, ", ")
}

// describeHook prints what a hook acts on, and what to weigh, before its
// action.
func describeHook(w io.Writer, out TrustOutput) {
	t := out.Trigger
	on := hooks.On{Watcher: t.Watcher, Type: t.Type}
	fmt.Fprintf(w, "%s is a hook; it acts on %s", out.Routine, on.String())
	if t.Batch {
		fmt.Fprint(w, ", in batches")
	}
	fmt.Fprintln(w, ".")
	if wt := out.Watcher; wt != nil {
		switch {
		case wt.Trusted:
			fmt.Fprintf(w, "Its watcher %s is trusted.\n", wt.Name)
		case wt.review == nil || wt.review.Approval == nil:
			fmt.Fprintf(w, "Its watcher %s can't be trusted: %s.\n", wt.Name, wt.Untrusted)
		default:
			fmt.Fprintf(w, "Its watcher %s isn't trusted (%s); trusting the hook trusts it too. It runs:\n", wt.Name, wt.Untrusted)
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "  executable\t%s\n", wt.Program.Executable)
			fmt.Fprintf(tw, "  sha-256\t%s\n", wt.Program.Checksum)
			if len(wt.Program.Args) > 0 {
				fmt.Fprintf(tw, "  arguments\t%s\n", strings.Join(wt.Program.Args, " "))
			}
			for _, s := range wt.Program.Secrets {
				fmt.Fprintf(tw, "  secret\t%s (as $%s)\n", s.Name, s.Env)
			}
			_ = tw.Flush()
			if len(wt.Changes) > 0 {
				fmt.Fprintf(w, "Since you last trusted it, it %s.\n", strings.Join(wt.Changes, "; "))
			}
		}
	}

	for _, warn := range out.Warnings {
		fmt.Fprintf(w, "Warning: %s.\n", warn)
	}
	fmt.Fprintln(w)
}
