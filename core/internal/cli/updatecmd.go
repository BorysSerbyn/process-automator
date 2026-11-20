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
	"github.com/BorysSerbyn/process-automator/core/internal/notify"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
	"github.com/BorysSerbyn/process-automator/core/internal/version"
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

// installNote is UpdateOutput.Note.
const installNote = "It downloads the release, checks it's genuine and installs it, in under a minute; " +
	"the app restarts when it's done."

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
			annReadOnlyWith:   "check",
			annNotForRoutines: "routines have no use for the app's own updates (checking runs git)",
		},
		Short: "Install the latest release; --check only reports",
		Long: "Asks GitHub for the latest release, and if it's newer than this build, downloads its artifact " +
			"for this platform, verifies it (its checksum; on macOS its notarized Developer ID signature) and " +
			"installs it in place of this app, restarting what's running. Until a published release has a " +
			"download for this machine, it builds the newest release tag from the source checkout instead " +
			"(update.source-dir). A dev build newer than the latest release is left alone. A private repository needs a GitHub sign-in " +
			"(`gh auth login`, or GH_TOKEN). An app installed by a Homebrew formula is updated with brew.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
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
				}
			}
			plan, err := planUpdate(cmd.Context(), a)
			if err != nil {
				return err
			}
			latest := plan.tag
			res := UpdateOutput{Current: version.Version, Latest: latest, UpdateAvailable: update.Newer(version.Version, latest)}
			if res.UpdateAvailable {
				res.Note = installNote
			}
			if check {
				res.InProgress = updating(a)
			}
			var appRoot string
			if res.UpdateAvailable && !check && plan.release != nil {
				if appRoot, _, err = installedApp(); err != nil {
					return err
				}
			}
			if res.UpdateAvailable && !check && background {
				if err := startDetachedUpdate(a); err != nil {
					return err
				}
				res.Started, res.InProgress = true, true
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), res)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Installing %s in the background; the app restarts when it's done (log: %s).\n", latest, a.Layout.UpdateLog())
				return nil
			}
			detached := os.Getenv(detachedEnv) == "1"
			if res.UpdateAvailable && !check {
				out := cmd.ErrOrStderr()
				if err := plan.install(cmd.Context(), appRoot, out); err != nil {
					if detached {
						recordApp(a, "Update to "+latest+" failed", err.Error())
					}
					return err
				}
				res.Updated = true
				if detached {
					recordApp(a, "Process Automator updated to "+strings.TrimPrefix(latest, "v"), "")
				}
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
	return cmd
}

func describeUpdate(r UpdateOutput) string {
	switch {
	case r.Latest == "":
		return fmt.Sprintf("No releases yet (this is %s).", r.Current)
	case r.Updated:
		return fmt.Sprintf("Updated %s → %s.", r.Current, strings.TrimPrefix(r.Latest, "v"))
	case r.UpdateAvailable:
		return fmt.Sprintf("%s is available (this is %s). Run `procauto update` to install it.", r.Latest, r.Current)
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

func planUpdate(ctx context.Context, a *engine.App) (updatePlan, error) {
	src := releaseSource(ctx)
	rel, relErr := src.Latest(ctx)
	if relErr == nil && rel.Tag != "" {
		if name, err := update.AssetName(rel.Version()); err == nil {
			if _, ok := rel.Asset(name); ok {
				return updatePlan{tag: rel.Tag, release: &rel, src: src}, nil
			}
		}
	}
	dir, err := sourceDir(a)
	if err != nil {
		if relErr != nil {
			return updatePlan{}, errors.Join(relErr, err)
		}
		return updatePlan{}, fmt.Errorf("no published release has a download for this machine, and %w", err)
	}
	tag, err := update.SourceLatest(ctx, dir)
	if err != nil {
		return updatePlan{}, err
	}
	return updatePlan{tag: tag, dir: dir}, nil
}

func (p updatePlan) install(ctx context.Context, appRoot string, out io.Writer) error {
	if p.release != nil {
		fmt.Fprintf(out, "Installing %s into %s…\n", p.tag, appRoot)
		return p.src.Install(ctx, *p.release, appRoot, version.TeamID, out)
	}
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
	if version.SourceDir == "" {
		return "", errors.New("no source checkout is known to build from: set one with `procauto settings set update.source-dir <path>`")
	}
	return version.SourceDir, nil
}

// releaseSource is where this build's releases are, signed in to GitHub
// when possible (a private repository needs it).
func releaseSource(ctx context.Context) update.Source {
	return update.Source{API: version.ReleaseAPI, Repo: version.ReleaseRepo, Token: update.Token(ctx)}
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
	if _, err := notify.Add(a.Layout.Notifications(), notify.Notification{Kind: notify.KindApp, Title: title, Body: body}, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}
