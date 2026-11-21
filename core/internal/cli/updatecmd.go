package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
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

// The notes UpdateOutput.Note gives: a release's download, or a build from
// the source checkout.
const (
	installNote = "It downloads the release, checks it's genuine and installs it, in under a minute; " +
		"the app restarts when it's done."
	buildNote = "It builds the release from the source checkout and installs it, which takes a few minutes; " +
		"the app restarts when it's done."
)

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

// startGrace is how long an update that hasn't finished counts as
// installing without holding the lock: a detached one between `update
// --detach` and the update it starts taking the lock.
const startGrace = 30 * time.Second

// updateProgress is where the latest update stands.
func updateProgress(a *engine.App, now time.Time) (UpdateProgress, error) {
	out := UpdateProgress{State: "none", Log: a.Layout.UpdateLog()}
	p, ok, err := update.ReadProgress(a.Layout.UpdateProgress())
	if err != nil {
		return out, err
	}
	held := updating(a)
	if !ok {
		if held {
			out.State = "installing"
		}
		return out, nil
	}
	out.Tag, out.Step, out.Detail, out.Error = p.Tag, p.Step, p.Detail, p.Error
	out.StartedAt, out.FinishedAt = &p.StartedAt, p.FinishedAt
	switch {
	case held || (p.FinishedAt == nil && now.Sub(p.UpdatedAt) < startGrace):
		out.State, out.FinishedAt, out.Error = "installing", nil, ""
	case p.FinishedAt == nil:
		out.State = "failed"
		out.Error = "it stopped before it finished (log: " + a.Layout.UpdateLog() + ")"
	case p.Error != "":
		out.State = "failed"
	default:
		out.State = "done"
	}
	return out, nil
}

func newUpdateProgressCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "progress",
		Short:       "Show what the update installing is doing, or how the latest one ended",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			p, err := updateProgress(a, time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), p)
			}
			fmt.Fprintln(cmd.OutOrStdout(), describeProgress(p))
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, UpdateProgress{})
	return cmd
}

func describeProgress(p UpdateProgress) string {
	switch p.State {
	case "none":
		return "No update has run."
	case "failed":
		return "The update failed: " + p.Error
	case "done":
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

// updating reports whether an update is installing: its lock is held.
func updating(a *engine.App) bool {
	unlock, err := filelock.Lock(a.Layout.UpdateLock(), false)
	if err != nil {
		return errors.Is(err, filelock.ErrHeld)
	}
	unlock()
	return false
}

// detachedEnv marks an update running detached: it reports its result as a
// notification, since whoever started it may be gone (automator-ui restarts).
const detachedEnv = "PROCAUTO_DETACHED_UPDATE"

func newUpdateCmd() *cobra.Command {
	var check, asJSON, background bool
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			detached := os.Getenv(detachedEnv) == "1"
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
			// failed records an update's failure: in its progress, and as a
			// notification when it runs detached.
			failed := func(what string, err error) error {
				progress.Finish("", err)
				if detached {
					recordApp(a, what, err.Error())
				}
				return err
			}
			plan, err := planUpdate(cmd.Context(), a)
			if err != nil {
				return failed("Couldn't update", err)
			}
			latest := plan.tag
			progress.Tag(latest)
			res := UpdateOutput{Current: product.Version, Latest: latest, UpdateAvailable: update.Newer(product.Version, latest)}
			if res.UpdateAvailable {
				res.Note = plan.note()
			}
			if check {
				res.InProgress = updating(a)
			}
			var appRoot string
			if res.UpdateAvailable && !check && plan.release != nil {
				if appRoot, _, err = installedApp(); err != nil {
					return failed("Update to "+latest+" failed", err)
				}
			}
			if res.UpdateAvailable && !check && background {
				started := update.NewReporter(a.Layout.UpdateProgress(), "Starting the update")
				started.Tag(latest)
				if err := startDetachedUpdate(a); err != nil {
					started.Finish("", err)
					return err
				}
				res.Started, res.InProgress = true, true
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), res)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Installing %s in the background; the app restarts when it's done (`%s update progress` follows it; log: %s).\n",
					latest, product.Command, a.Layout.UpdateLog())
				return nil
			}
			if res.UpdateAvailable && !check {
				if err := plan.install(cmd.Context(), appRoot, progress, io.MultiWriter(cmd.ErrOrStderr(), progress)); err != nil {
					return failed("Update to "+latest+" failed", err)
				}
				res.Updated = true
				if detached {
					recordApp(a, product.Name+" updated to "+strings.TrimPrefix(latest, "v"), "")
				}
			}
			if !check {
				progress.Finish(strings.TrimSuffix(describeUpdate(res), "."), nil)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			fmt.Fprintln(cmd.OutOrStdout(), describeUpdate(res))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	cmd.Flags().BoolVar(&background, "detach", false, "install in the background and return at once; the result comes as a notification (frontends)")
	jsonFlag(cmd.Flags(), &asJSON, UpdateOutput{})
	cmd.AddCommand(newUpdateProgressCmd())
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

// updatePlan is where the latest release comes from: a published release
// with a download for this machine, or else the source checkout's tags
// (built with `make install`).
type updatePlan struct {
	tag     string
	release *update.Release // nil: build from source
	src     update.Source
	dir     string
}

// planUpdate finds the latest release. The source checkout is the fallback
// only when the releases were read and none has a download for this
// machine (or there are none); when they can't be read (no access, no
// network), that's the answer.
func planUpdate(ctx context.Context, a *engine.App) (updatePlan, error) {
	src := releaseSource(ctx)
	rel, err := src.Latest(ctx)
	if err != nil {
		return updatePlan{}, fmt.Errorf("can't read the releases: %w", err)
	}
	if rel.Tag != "" {
		if name, err := update.AssetName(rel.Version()); err == nil {
			if _, ok := rel.Asset(name); ok {
				return updatePlan{tag: rel.Tag, release: &rel, src: src}, nil
			}
		}
	}
	dir, err := sourceDir(a)
	if err != nil {
		return updatePlan{}, fmt.Errorf("no published release has a download for this machine, and %w", err)
	}
	tag, err := update.SourceLatest(ctx, dir)
	if err != nil {
		return updatePlan{}, err
	}
	return updatePlan{tag: tag, dir: dir}, nil
}

// note is UpdateOutput.Note: what installing involves.
func (p updatePlan) note() string {
	if p.release != nil {
		return installNote
	}
	return buildNote
}

// install installs the plan's release, telling progress each step.
func (p updatePlan) install(ctx context.Context, appRoot string, progress *update.Reporter, out io.Writer) error {
	if p.release != nil {
		fmt.Fprintf(out, "Installing %s into %s…\n", p.tag, appRoot)
		return p.src.Install(ctx, *p.release, appRoot, product.TeamID, progress.Step, out)
	}
	progress.Step("Building " + p.tag + " from the source checkout")
	fmt.Fprintf(out, "Building %s from %s…\n", p.tag, p.dir)
	return update.BuildInstall(ctx, p.dir, p.tag, out)
}

// sourceDir is the checkout to build releases from: the setting, else the
// checkout this build came from.
func sourceDir(a *engine.App) (string, error) {
	if vals, err := a.Settings(); err == nil {
		if dir := vals.String("update.source-dir"); dir != "" {
			return dir, nil
		}
	}
	if product.SourceDir == "" {
		return "", errors.New("no source checkout is known to build from: set one with `" + product.Command + " settings set update.source-dir <path>`")
	}
	return product.SourceDir, nil
}

// findReleaseToken finds a GitHub token that can read private releases
// (tests replace it: they never run the real gh).
var findReleaseToken = update.Token

// releaseSource is where this build's releases are. Public ones are read
// from GitHub's web pages with no token: no sign-in is looked for. Private
// ones are read signed in to GitHub with an account that can read them,
// when there's one.
func releaseSource(ctx context.Context) update.Source {
	if product.PublicReleases {
		return update.Source{Web: product.ReleaseWeb, Repo: product.ReleaseRepo}
	}
	token, accounts := findReleaseToken(ctx, product.ReleaseAPI, product.ReleaseRepo)
	return update.Source{API: product.ReleaseAPI, Repo: product.ReleaseRepo, Token: token, Accounts: accounts}
}

// releasesHelp says how update reads the releases, for its help.
func releasesHelp() string {
	if product.PublicReleases {
		return "The releases are public: they're read from GitHub with no sign-in."
	}
	return "The releases are private, so they're read with a GitHub sign-in: GH_TOKEN, or an account `gh` is " +
		"signed in to that can read them (the active one first, then the others)."
}

// startDetachedUpdate runs this update again in the background, detached, so
// it survives the app restarting, logging to update.log.
func startDetachedUpdate(a *engine.App) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(a.Layout.UpdateLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(exe, "--config", a.Layout.Config(), "update")
	cmd.Env = append(os.Environ(), detachedEnv+"=1")
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// recordApp records a notification about the app itself.
func recordApp(a *engine.App, title, body string) {
	if _, err := notifications.Add(a.Layout.Notifications(), notifications.Notification{Kind: notifications.KindApp, Title: title, Body: body}, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}
