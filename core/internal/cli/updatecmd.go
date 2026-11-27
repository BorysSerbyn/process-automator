package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
)

// UpdateOutput is `update --json`.
type UpdateOutput struct {
	Current string `json:"current"`
	// Latest is the newest release tag, empty when there are none.
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Updated         bool   `json:"updated"`
	// Started is true when --detach started the update in the background.
	Started bool `json:"started,omitempty"`
	// InProgress is true while an update is installing (this one, or one
	// started before).
	InProgress bool `json:"inProgress"`
	// Note says what installing involves, for frontends to show before they
	// start one (set when an update is available).
	Note string `json:"note,omitempty"`
}

// UpdateProgress is `update progress --json`: the latest update, as it goes.
type UpdateProgress struct {
	// State is none (no update has run), installing, done or failed.
	State string `json:"state" jsonschema:"enum=none,enum=installing,enum=done,enum=failed"`
	// Tag is the release being installed, once known.
	Tag string `json:"tag,omitempty"`
	// Step is what it's doing (or last did), in words.
	Step string `json:"step,omitempty"`
	// Detail is the latest line of the step's output (a build's), if any.
	Detail     string     `json:"detail,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// Error is why it failed.
	Error string `json:"error,omitempty"`
	// Log is the file a detached update writes its whole output to.
	Log string `json:"log"`
}

// updateProgress is where the latest update stands.
func updateProgress(a *engine.App, now time.Time) (UpdateProgress, error) {
	state, p, err := update.Latest(a.Layout.UpdateProgress(), a.Layout.UpdateLock(), a.Layout.UpdateLog(), now)
	out := UpdateProgress{State: state, Tag: p.Tag, Step: p.Step, Detail: p.Detail, FinishedAt: p.FinishedAt, Error: p.Error,
		Log: a.Layout.UpdateLog()}
	if !p.StartedAt.IsZero() {
		out.StartedAt = &p.StartedAt
	}
	return out, err
}

func newUpdateProgressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "progress",
		Short:       "Show what the update installing is doing, or how the latest one ended",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			p, err := updateProgress(a, time.Now())
			if err != nil {
				return err
			}
			return emit(cmd, p, describeProgress(p))
		}),
	}
	jsonFlag(cmd.Flags(), UpdateProgress{})
	return cmd
}

func describeProgress(p UpdateProgress) string {
	switch p.State {
	case update.StateNone:
		return "No update has run."
	case update.StateFailed:
		return "The update failed: " + p.Error
	case update.StateDone:
		return p.Step + "."
	}
	text := "Updating"
	if p.Tag != "" {
		text += " to " + p.Tag
	}
	if p.Step != "" {
		text += ": " + p.Step
	}
	if p.Detail != "" {
		text += "\n  " + p.Detail
	}
	return text
}

// askable is whether `update --ask` has a terminal to ask in (tests fake one).
var askable = func() bool { return isTerminal(os.Stdin) }

// askFirst asks in the terminal before installing (`update --ask`): it says
// what's available and what installing involves. install is false when
// there's nothing to install or the answer was no, which it has said.
func askFirst(cmd *cobra.Command, a *engine.App) (install bool, err error) {
	if !askable() {
		return false, errors.New("--ask needs a terminal to ask in")
	}
	plan, err := a.PlanUpdate(cmd.Context())
	if err != nil {
		return false, err
	}
	res := UpdateOutput{Current: product.Version, Latest: plan.Tag, UpdateAvailable: update.Newer(product.Version, plan.Tag)}
	out := cmd.OutOrStdout()
	if !res.UpdateAvailable {
		fmt.Fprintln(out, describeUpdate(res))
		return false, nil
	}
	if !newPrompt(cmd).Confirm(fmt.Sprintf("%s is available (this is %s). %s\nInstall it now?", plan.Tag, product.Version, plan.Note()), false) {
		fmt.Fprintln(out, "Not updated.")
		return false, nil
	}
	return true, nil
}

func newUpdateCmd() *cobra.Command {
	var check, background, ask bool
	cmd := &cobra.Command{
		Use: "update",
		Annotations: map[string]string{
			annReadOnlyWith: "check",
			annNotReadOnly:  "checking for updates runs git and reaches the network",
		},
		Short: "Install the latest release; --check only reports",
		Long: "Asks GitHub for the latest release, and if it's newer than this build, downloads its artifact " +
			"for this platform, verifies it (its checksum; on macOS its notarized Developer ID signature) and " +
			"installs it in place of this app, restarting what's running. When the releases can be read but none " +
			"has a download for this machine, it builds the newest release tag from the source checkout instead " +
			"(update.source-dir); when they can't be read, it says why and stops. A dev build newer than the latest " +
			"release is left alone. " + releasesHelp() + " An app installed by a Homebrew " +
			"formula is updated with brew. `update progress` says what an update installing is doing.",
		Args: cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			if ask && !check {
				if install, err := askFirst(cmd, a); err != nil || !install {
					return err
				}
			}
			detached := os.Getenv(engine.DetachedUpdateEnv) == "1"
			automatic := detached && os.Getenv(engine.AutoUpdateEnv) == "1"
			// progress records what an update installing does, for `update
			// progress`; checking records nothing.
			progress := update.NewReporter("", "")
			if !check {
				// Installing holds the lock from the start (a detached update
				// too, as soon as it runs), so "in progress" has no gap.
				unlock, err := filelock.Lock(a.Layout.UpdateLock(), false)
				if errors.Is(err, filelock.ErrHeld) {
					return errors.New("an update is already installing (log: " + a.Layout.UpdateLog() + ")")
				}
				if err != nil {
					return err
				}
				if background {
					unlock() // the detached update takes it
				} else {
					defer unlock()
					progress = update.NewReporter(a.Layout.UpdateProgress(), "Checking for the latest release")
				}
			}
			latest := ""
			// failed records an update's failure: in its progress, and as a
			// notification when it runs detached (an automatic one says the
			// version I have keeps working).
			failed := func(err error) error {
				progress.Finish("", err)
				if detached {
					title, body := "Couldn't update", err.Error()
					if latest != "" {
						title = "Couldn't update to " + latest
					}
					if automatic {
						title += " automatically"
						body += "\n" + product.Name + " " + product.Version + " keeps working."
					}
					a.RecordUpdate(title, body)
				}
				return err
			}
			plan, err := a.PlanUpdate(cmd.Context())
			if err != nil {
				return failed(err)
			}
			latest = plan.Tag
			progress.Tag(latest)
			res := UpdateOutput{Current: product.Version, Latest: latest, UpdateAvailable: update.Newer(product.Version, latest)}
			if res.UpdateAvailable {
				res.Note = plan.Note()
			}
			if check {
				res.InProgress = update.Installing(a.Layout.UpdateLock())
			}
			if res.UpdateAvailable && automatic && !plan.Downloads() {
				return failed(errors.New(latest + " has no download for this computer, and automatic updates install only downloads (`" +
					product.Command + " update` builds it from the source checkout)"))
			}
			var appRoot string
			if res.UpdateAvailable && !check && plan.Downloads() {
				if appRoot, _, err = engine.InstalledApp(); err != nil {
					return failed(err)
				}
			}
			if res.UpdateAvailable && !check && background {
				started := update.NewReporter(a.Layout.UpdateProgress(), "Starting the update")
				started.Tag(latest)
				if err := a.StartDetachedUpdate(false); err != nil {
					started.Finish("", err)
					return err
				}
				res.Started, res.InProgress = true, true
				return emit(cmd, res, fmt.Sprintf("Installing %s in the background; the app restarts when it's done (`%s update progress` follows it; log: %s).",
					latest, product.Command, a.Layout.UpdateLog()))
			}
			if res.UpdateAvailable && !check {
				if err := plan.Install(cmd.Context(), appRoot, progress.Step, io.MultiWriter(cmd.ErrOrStderr(), progress)); err != nil {
					return failed(err)
				}
				res.Updated = true
				if detached {
					title := product.Name + " updated to " + strings.TrimPrefix(latest, "v")
					body := "Click to see what's new."
					if automatic {
						body = "Installed automatically. " + body
					}
					a.RecordUpdate(title, body)
				}
			}
			if !check {
				progress.Finish(strings.TrimSuffix(describeUpdate(res), "."), nil)
			}
			return emit(cmd, res, describeUpdate(res))
		}),
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	cmd.Flags().BoolVar(&background, "detach", false, "install in the background and return at once; the result comes as a notification (frontends)")
	cmd.Flags().BoolVar(&ask, "ask", false, "say what's available and ask in the terminal before installing it (frontends that can't ask themselves)")
	jsonFlag(cmd.Flags(), UpdateOutput{})
	cmd.AddCommand(newUpdateProgressCmd(), newUpdateStatusCmd(), newUpdateNotesCmd())
	return cmd
}

func describeUpdate(r UpdateOutput) string {
	switch {
	case r.Latest == "":
		return fmt.Sprintf("No releases yet (this is %s).", r.Current)
	case r.Updated:
		return fmt.Sprintf("Updated %s → %s.", r.Current, strings.TrimPrefix(r.Latest, "v"))
	case r.UpdateAvailable:
		return fmt.Sprintf("%s is available (this is %s). Run `"+product.Command+" update` to install it.", r.Latest, r.Current)
	default:
		return fmt.Sprintf("Up to date (this is %s; latest release %s).", r.Current, r.Latest)
	}
}

// releasesHelp says how update reads the releases, for its help.
func releasesHelp() string {
	if product.PublicReleases {
		return "The releases are public: they're read from GitHub with no sign-in."
	}
	return "The releases are private, so they're read with a GitHub sign-in: GH_TOKEN, or an account `gh` is " +
		"signed in to that can read them (the active one first, then the others)."
}

func newUpdateStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show where updates stand: this version, the latest release, and automatic updates",
		Long: "This version, whether automatic updates are on (the " + engine.AutoUpdateSetting + " setting) and apply " +
			"to this install, when the scheduler last checked for a newer release and what it found, why that release " +
			"isn't installing by itself yet, and whether an update is installing. It asks GitHub nothing: `update " +
			"--check` does, and the scheduler does every hour.",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			st, err := a.UpdateStatus(time.Now())
			if err != nil {
				return err
			}
			return emit(cmd, st, describeStatus(st))
		}),
	}
	jsonFlag(cmd.Flags(), engine.UpdateStatus{})
	return cmd
}

func describeStatus(st engine.UpdateStatus) string {
	lines := []string{"This is " + st.Current + "."}
	switch {
	case st.Installing && st.InstallingTag != "":
		lines = append(lines, "Updating to "+st.InstallingTag+" (`"+product.Command+" update progress` follows it).")
	case st.Installing:
		lines = append(lines, "An update is installing (`"+product.Command+" update progress` follows it).")
	case st.UpdateAvailable:
		lines = append(lines, st.Latest+" is available.")
	case st.Latest != "":
		lines = append(lines, "Up to date (latest release "+st.Latest+").")
	}
	auto := "Automatic updates are off (" + st.Setting + ")."
	if st.Automatic {
		auto = "Automatic updates are on."
	}
	lines = append(lines, auto)
	if st.Automatic && st.AutomaticBlocked != "" {
		lines = append(lines, "They don't apply here: "+st.AutomaticBlocked+".")
	}
	if st.Waiting != "" {
		lines = append(lines, st.Waiting+".")
	}
	if st.CheckedAt != nil {
		checked := "Last checked " + st.CheckedAt.Local().Format("Jan 2 15:04")
		if st.CheckError != "" {
			checked += ", which failed: " + st.CheckError
		}
		lines = append(lines, checked+".")
	}
	return strings.Join(lines, "\n")
}

// NotesOutput is `update notes --json`: a release's notes.
type NotesOutput struct {
	Tag string `json:"tag"`
	// Notes are the release's notes as written on GitHub, in Markdown;
	// empty when it has none.
	Notes string `json:"notes"`
	// URL is the release's page on GitHub.
	URL         string     `json:"url,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

func newUpdateNotesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notes [<release>]",
		Short: "Show a release's notes (this version's by default)",
		Long: "The release's notes, as its GitHub release has them (Markdown), read from GitHub at most once a day: " +
			"this version's, or the release named (v1.2.0). A development build has none.",
		Annotations: readOnly(),
		Args:        cobra.MaximumNArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			tag := ""
			if len(args) == 1 {
				tag = args[0]
			}
			n, err := a.ReleaseNotes(cmd.Context(), tag)
			if err != nil {
				return err
			}
			out := NotesOutput{Tag: n.Tag, Notes: n.Body, URL: n.URL, PublishedAt: n.PublishedAt}
			text := n.Body
			if text == "" {
				text = n.Tag + " has no notes."
			}
			return emit(cmd, out, text)
		}),
	}
	jsonFlag(cmd.Flags(), NotesOutput{})
	return cmd
}
