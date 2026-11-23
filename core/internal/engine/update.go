package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/notifications"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
)

// Updates: where releases come from, what installing the latest involves,
// where updates stand (UpdateStatus) and starting one in the background.
// The scheduler installs them by itself too (autoupdate.go).

// AutoUpdateSetting turns automatic updates on and off.
const AutoUpdateSetting = "update.auto"

// FindReleaseToken finds a GitHub token that can read private releases
// (tests replace it: they never run the real gh).
var FindReleaseToken = update.Token

// ReleaseSource is where this build's releases are. Public ones are read
// from GitHub's web pages with no token: no sign-in is looked for. Private
// ones are read signed in to GitHub with an account that can read them,
// when there's one.
func ReleaseSource(ctx context.Context) update.Source {
	if product.PublicReleases {
		return update.Source{Web: product.ReleaseWeb, API: product.ReleaseAPI, Repo: product.ReleaseRepo}
	}
	token, accounts := FindReleaseToken(ctx, product.ReleaseAPI, product.ReleaseRepo)
	return update.Source{API: product.ReleaseAPI, Repo: product.ReleaseRepo, Token: token, Accounts: accounts}
}

// UpdatePlan is where the latest release comes from: a published release
// with a download for this machine, or else the source checkout's tags
// (built with `make install`).
type UpdatePlan struct {
	// Tag is the latest release ("" when there are none).
	Tag     string
	release *update.Release // nil: build from source
	src     update.Source
	dir     string
}

// Downloads is whether the plan installs a published download (not a build
// from the source checkout).
func (p UpdatePlan) Downloads() bool { return p.release != nil }

// The notes UpdatePlan.Note gives: a release's download, or a build from
// the source checkout.
const (
	InstallNote = "It downloads the release, checks it's genuine and installs it, in under a minute; " +
		"the app restarts when it's done."
	BuildNote = "It builds the release from the source checkout and installs it, which takes a few minutes; " +
		"the app restarts when it's done."
)

// Note is what installing involves, for frontends to show before they start one.
func (p UpdatePlan) Note() string {
	if p.release != nil {
		return InstallNote
	}
	return BuildNote
}

// PlanUpdate finds the latest release, and records what it found for
// automatic updates (update.RecordCheck). The source checkout is the
// fallback only when the releases were read and none has a download for
// this machine (or there are none); when they can't be read (no access, no
// network), that's the answer.
func (a *App) PlanUpdate(ctx context.Context) (UpdatePlan, error) {
	plan, err := a.planUpdate(ctx)
	if rerr := update.RecordCheck(a.Layout.UpdateAuto(), plan.Tag, plan.Downloads(), err, time.Now()); rerr != nil {
		logf("update: %v", rerr)
	}
	return plan, err
}

func (a *App) planUpdate(ctx context.Context) (UpdatePlan, error) {
	src := ReleaseSource(ctx)
	rel, err := src.Latest(ctx)
	if err != nil {
		return UpdatePlan{}, fmt.Errorf("can't read the releases: %w", err)
	}
	if rel.Tag != "" {
		if name, err := update.AssetName(rel.Version()); err == nil {
			if _, ok := rel.Asset(name); ok {
				return UpdatePlan{Tag: rel.Tag, release: &rel, src: src}, nil
			}
		}
	}
	dir, err := a.sourceDir()
	if err != nil {
		return UpdatePlan{}, fmt.Errorf("no published release has a download for this machine, and %w", err)
	}
	tag, err := update.SourceLatest(ctx, dir)
	if err != nil {
		return UpdatePlan{}, err
	}
	return UpdatePlan{Tag: tag, dir: dir}, nil
}

// Install installs the plan's release into appRoot (InstalledApp's root;
// unused for a build from source), telling step each step as it starts.
func (p UpdatePlan) Install(ctx context.Context, appRoot string, step func(string), out io.Writer) error {
	if p.release != nil {
		fmt.Fprintf(out, "Installing %s into %s…\n", p.Tag, appRoot)
		return p.src.Install(ctx, *p.release, appRoot, product.TeamID, step, out)
	}
	step("Building " + p.Tag + " from the source checkout")
	fmt.Fprintf(out, "Building %s from %s…\n", p.Tag, p.dir)
	return update.BuildInstall(ctx, p.dir, p.Tag, out)
}

// sourceDir is the checkout to build releases from: the setting, else the
// checkout this build came from.
func (a *App) sourceDir() (string, error) {
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

// InstalledApp is the app this binary is part of (the .app, or the Linux
// tree) and its installer, which sits next to the templates two folders
// below the app's root in both layouts. A development build has none, and
// one Homebrew's formula installed is brew's to update and remove.
func InstalledApp() (root, installer string, err error) {
	templates, err := TemplatesDir()
	if err != nil {
		return "", "", err
	}
	resources := filepath.Dir(templates)
	installer = filepath.Join(resources, update.InstallerName)
	if _, err := os.Stat(installer); err != nil {
		return "", "", errors.New("this build isn't an installed app (it has no " + update.InstallerName +
			"): install a release with install.sh, or build and install one with `make install`")
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil && strings.Contains(exe, string(filepath.Separator)+"Cellar"+string(filepath.Separator)) {
		return "", "", errors.New("this app was installed by Homebrew: update or remove it with brew " +
			"(`brew upgrade " + product.Folder + "`, `brew uninstall " + product.Folder + "`)")
	}
	return filepath.Dir(filepath.Dir(resources)), installer, nil
}

// Development is whether this is a development build ("1.2.1-dev+abc123"),
// not a release.
func Development() bool { return strings.Contains(product.Version, "-") }

// autoBlocked says why this install isn't updated automatically ("" when
// it is): a development build moves forward with `make install`, and one
// that isn't an installed app (or Homebrew's formula installed) can't be.
// A variable for tests.
var autoBlocked = func() string {
	if Development() {
		return "this is a development build: install releases with `" + product.Command + " update`, or build with `make install`"
	}
	if _, _, err := InstalledApp(); err != nil {
		return err.Error()
	}
	return ""
}

// UpdateStatus is where updates stand (`update status --json`), from what
// the app already knows: nothing is asked of GitHub.
type UpdateStatus struct {
	// Current is this build's version, Development whether it's a
	// development build rather than a release.
	Current     string `json:"current"`
	Development bool   `json:"development"`
	// Automatic is whether automatic updates are on (the update.auto
	// setting, named by Setting).
	Automatic bool   `json:"automatic"`
	Setting   string `json:"setting"`
	// AutomaticBlocked says why this install isn't updated automatically,
	// even with them on; empty when it is.
	AutomaticBlocked string `json:"automaticBlocked,omitempty"`
	// CheckedAt is when the latest release was last asked for, CheckError
	// why that failed.
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	CheckError string     `json:"checkError,omitempty"`
	// Latest is the newest release the last check found ("" when there are
	// none, or none was found yet); UpdateAvailable whether it's newer than
	// this build.
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	// Waiting says why the newer release isn't installing by itself yet (or
	// won't), in words, while automatic updates are on.
	Waiting string `json:"waiting,omitempty"`
	// Installing is true while an update installs, and InstallingTag the
	// release, once known (`update progress` says what it's doing).
	Installing    bool   `json:"installing"`
	InstallingTag string `json:"installingTag,omitempty"`
}

// UpdateStatus is where updates stand now.
func (a *App) UpdateStatus(now time.Time) (UpdateStatus, error) {
	st, err := update.LoadAuto(a.Layout.UpdateAuto())
	if err != nil {
		return UpdateStatus{}, err
	}
	out := UpdateStatus{
		Current: product.Version, Development: Development(), Setting: AutoUpdateSetting,
		AutomaticBlocked: autoBlocked(),
		CheckedAt:        st.CheckedAt, CheckError: st.CheckError,
		Latest: st.Latest, UpdateAvailable: update.Newer(product.Version, st.Latest),
	}
	if vals, err := a.Settings(); err == nil {
		out.Automatic = vals.Bool(AutoUpdateSetting)
	}
	state, p, err := update.Latest(a.Layout.UpdateProgress(), a.Layout.UpdateLock(), a.Layout.UpdateLog(), now)
	if err != nil {
		return out, err
	}
	out.Installing = state == update.StateInstalling
	if out.Installing {
		out.InstallingTag = p.Tag
	}
	if out.Automatic && out.AutomaticBlocked == "" && out.UpdateAvailable && !out.Installing {
		out.Waiting = st.Waiting
	}
	return out, nil
}

// ReleaseNotes are a release's notes (this build's when tag is ""), read
// from GitHub at most once a day (update.CachedNotes).
func (a *App) ReleaseNotes(ctx context.Context, tag string) (update.Notes, error) {
	if tag == "" {
		if Development() {
			return update.Notes{}, errors.New("this is a development build (" + product.Version + "): it has no release notes")
		}
		tag = "v" + product.Version
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return update.CachedNotes(ctx, ReleaseSource(ctx), a.Layout.ReleaseNotes(), tag, time.Now())
}

// PostponeUpdate holds an automatic update back for a few minutes: the app
// is in use (frontends say so while one of their windows is open).
func (a *App) PostponeUpdate(now time.Time) (time.Time, error) {
	return update.Postpone(a.Layout.UpdateAuto(), now)
}

// Environment of an update running detached.
const (
	// DetachedUpdateEnv marks an update running detached: it reports its
	// result as a notification, since whoever started it may be gone
	// (automator-ui restarts).
	DetachedUpdateEnv = "PROCAUTO_DETACHED_UPDATE"
	// AutoUpdateEnv marks one the scheduler started by itself: it installs
	// only a published download, and its notification says it was automatic.
	AutoUpdateEnv = "PROCAUTO_AUTO_UPDATE"
)

// updateCommand is the command a detached update runs: this executable's
// `update`, logging to update.log. A variable for tests, which never
// install.
var updateCommand = func(a *App, env []string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "--config", a.Layout.Config(), "update")
	cmd.Env = append(os.Environ(), env...)
	return cmd, nil
}

// StartDetachedUpdate runs `update` in the background, detached, so it
// survives the app restarting (and, started by the scheduler, the
// scheduler restarting), logging to update.log. automatic marks one the
// scheduler started by itself.
func (a *App) StartDetachedUpdate(automatic bool) error {
	env := []string{DetachedUpdateEnv + "=1"}
	if automatic {
		env = append(env, AutoUpdateEnv+"=1")
	}
	cmd, err := updateCommand(a, env)
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(a.Layout.UpdateLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	cmd.Stdout, cmd.Stderr = logf, logf
	return startDetached(cmd, a.Layout.UpdateLog())
}

// RecordUpdate records a notification about an update (installed, or
// failed): a click on it shows the app's updates.
func (a *App) RecordUpdate(title, body string) {
	n := notifications.Notification{Kind: notifications.KindApp, Update: true, Title: title, Body: body}
	if _, err := notifications.Add(a.Layout.Notifications(), n, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}
