package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// TemplateInfo is one row of `templates list --json`.
type TemplateInfo struct {
	templates.Template
	// RunsHere is false for templates for another OS (listed with --all).
	RunsHere bool `json:"runsHere"`
	// SupportsBackend is false for a template that doesn't work with the
	// selected backend (llm.backend): installing it is refused.
	SupportsBackend bool `json:"supportsBackend"`
	// Installed is true when one of its kind with the template's name exists
	// (a routine of either kind, a hook, a watcher).
	Installed bool `json:"installed"`
	// WithWatcher is a hook template's watcher template: installing the hook
	// installs it too while it isn't installed, so the two are one choice.
	// Its parameters are among the hook's (params) while it isn't
	// installed. A watcher template a hook comes with isn't listed on its
	// own.
	WithWatcher *TemplateWatcher `json:"withWatcher,omitempty"`
}

// TemplateWatcher is the watcher template a hook template comes with.
type TemplateWatcher struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Params      []routines.Param `json:"params,omitempty"`
	// Installed is true when the watcher is there already: installing the
	// hook then uses it.
	Installed bool `json:"installed"`
}

// Installable says the template can be installed now: for this OS and the
// selected backend, and not installed yet.
func (t TemplateInfo) Installable() bool { return t.RunsHere && t.SupportsBackend && !t.Installed }

// TemplatesDir is where the app's templates are: bundled next to the binary
// (product.TemplatesDir, relative to the executable), or the source checkout's
// in a development build.
func TemplatesDir() (string, error) {
	if dir := product.TemplatesDir; dir != "" {
		if filepath.IsAbs(dir) {
			return dir, nil
		}
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil { // the CLI is a symlink into the app
			return "", err
		}
		return filepath.Join(filepath.Dir(exe), dir), nil
	}
	if product.SourceDir != "" {
		return filepath.Join(product.SourceDir, "templates"), nil
	}
	return "", errors.New("this build doesn't know where its templates are")
}

// Templates is the templates for this OS (all: every OS), each saying
// whether one of its kind and name is installed.
func (a *App) Templates(all bool) ([]TemplateInfo, error) {
	dir, err := TemplatesDir()
	if err != nil {
		return nil, err
	}
	list, err := templates.List(dir)
	if err != nil {
		return nil, err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return nil, err
	}
	backend, err := a.BackendDef()
	if err != nil {
		return nil, err
	}
	// A hook and the watcher it listens to are one choice: the watcher
	// templates hooks come with aren't listed on their own.
	withWatcher := map[string]templates.Template{}
	comesWithHook := map[string]bool{}
	for _, t := range list {
		if t.Kind != templates.KindHook {
			continue
		}
		if w, err := templates.WatcherOf(list, t); err == nil {
			withWatcher[t.ID], comesWithHook[w.ID] = w, true
		}
	}
	rows := []TemplateInfo{}
	for _, t := range list {
		if comesWithHook[t.ID] {
			continue
		}
		row := TemplateInfo{Template: t, RunsHere: t.RunsHere(), SupportsBackend: t.Supports(backend.Name),
			Installed: a.templateInstalled(set, t)}
		if w, ok := withWatcher[t.ID]; ok {
			installed := watchers.Exists(a.Layout.Watchers(), w.Name)
			row.WithWatcher = &TemplateWatcher{ID: w.ID, Name: w.Name, Description: w.Description, Params: w.Params, Installed: installed}
			row.RunsHere = row.RunsHere && (installed || w.RunsHere())
			if !installed {
				row.Params = hookAndWatcherParams(t.Params, w.Params)
			}
		}
		if !all && !row.RunsHere {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// hookAndWatcherParams are a hook's parameters and those of the watcher installed
// with it, one for a name both declare (it takes one value).
func hookAndWatcherParams(hook, watcher []routines.Param) []routines.Param {
	out := slices.Clone(hook)
	for _, p := range watcher {
		if !slices.ContainsFunc(out, func(q routines.Param) bool { return q.Name == p.Name }) {
			out = append(out, p)
		}
	}
	return out
}

// templateInstalled reports whether one of t's kind and name is installed.
func (a *App) templateInstalled(set *routines.Set, t templates.Template) bool {
	switch t.Kind {
	case templates.KindProgram:
		return programs.Exists(a.Layout.Programs(), t.Name)
	case templates.KindHook:
		return hooks.Exists(a.Layout.Hooks(), t.Name)
	case templates.KindWatcher:
		return watchers.Exists(a.Layout.Watchers(), t.Name)
	}
	_, err := set.Find(t.Name)
	return err == nil
}

// TemplatePlan is what installing a template adds: it, and for a hook the
// watcher it listens to when that isn't installed (from its template), with
// the parameters of both.
type TemplatePlan struct {
	Template templates.Template
	// Watcher is the watcher template installed first; nil when there's none
	// to install.
	Watcher *templates.Template
	Params  []routines.Param
}

// PlanTemplate finds the template id and works out what installing it
// adds. It refuses one for another OS, or the selected backend's.
func (a *App) PlanTemplate(id string) (TemplatePlan, error) {
	dir, err := TemplatesDir()
	if err != nil {
		return TemplatePlan{}, err
	}
	t, err := templates.Find(dir, id)
	if err != nil {
		return TemplatePlan{}, err
	}
	if !t.RunsHere() {
		return TemplatePlan{}, fmt.Errorf("%s runs on %s, not %s", t.ID, strings.Join(t.Platforms, ", "), routines.Platform())
	}
	backend, err := a.BackendDef()
	if err != nil {
		return TemplatePlan{}, err
	}
	if !t.Supports(backend.Name) {
		return TemplatePlan{}, fmt.Errorf("%s works only with the %s backend, and llm.backend is %s: switch with `%s settings set llm.backend %s` first",
			t.ID, strings.Join(t.Backends, " or "), backend.Name, product.Command, t.Backends[0])
	}
	plan := TemplatePlan{Template: t, Params: slices.Clone(t.Params)}
	if t.Kind != templates.KindHook || watchers.Exists(a.Layout.Watchers(), t.Watcher) {
		return plan, nil
	}
	all, err := templates.List(dir)
	if err != nil {
		return TemplatePlan{}, err
	}
	w, err := templates.WatcherOf(all, t)
	if err != nil {
		return TemplatePlan{}, fmt.Errorf("%s: %w; add the watcher %s first", t.ID, err, t.Watcher)
	}
	if !w.RunsHere() {
		return TemplatePlan{}, fmt.Errorf("%s listens to the watcher %s, which runs on %s, not %s", t.ID, w.ID, strings.Join(w.Platforms, ", "), routines.Platform())
	}
	plan.Watcher = &w
	plan.Params = hookAndWatcherParams(t.Params, w.Params)
	return plan, nil
}

// Added is one thing installing a template added.
type Added struct {
	// Kind is program, hook or watcher.
	Kind string `json:"kind" jsonschema:"enum=program,enum=hook,enum=watcher"`
	Name string `json:"name"`
	// Path is its file in the app's folders.
	Path string `json:"path"`
}

// InstallFolderTemplate installs a program, hook or watcher template as
// name, as adding one from its files does (off and untrusted): first the
// watcher the plan installs with a hook, then it. values are the
// parameters' (missing ones take their defaults), filled into each's
// description file; an executable with a build is built first (its output
// goes to log). A hook that fails to be added takes the watcher added with
// it away again.
func (a *App) InstallFolderTemplate(ctx context.Context, plan TemplatePlan, name string, values map[string]string, log io.Writer) ([]Added, error) {
	var added []Added
	if w := plan.Watcher; w != nil {
		one, err := a.installFolder(ctx, *w, w.Name, values, log)
		if err != nil {
			return nil, fmt.Errorf("installing the watcher %s (%s), which %s listens to: %w", w.Name, w.ID, plan.Template.ID, err)
		}
		added = append(added, one)
	}
	one, err := a.installFolder(ctx, plan.Template, name, values, log)
	if err != nil {
		if plan.Watcher != nil {
			_, _ = watchers.Delete(a.Layout.Watchers(), plan.Watcher.Name)
		}
		return nil, err
	}
	return append(added, one), nil
}

// installFolder stages t and adds it as name.
func (a *App) installFolder(ctx context.Context, t templates.Template, name string, values map[string]string, log io.Writer) (Added, error) {
	tmp, err := os.MkdirTemp("", "template-")
	if err != nil {
		return Added{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	desc, err := t.Stage(ctx, values, tmp, log)
	if err != nil {
		return Added{}, err
	}
	out := Added{Kind: t.Kind, Name: name}
	switch t.Kind {
	case templates.KindProgram:
		p, err := a.AddProgram(name, desc, false)
		out.Path = p.Path
		return out, err
	case templates.KindHook:
		h, err := a.AddHook(name, desc, false)
		out.Path = h.Path
		return out, err
	case templates.KindWatcher:
		w, err := a.AddWatcher(name, desc, false)
		out.Path = w.Path
		return out, err
	}
	return Added{}, fmt.Errorf("%s is a routine template, not a folder", t.ID)
}
