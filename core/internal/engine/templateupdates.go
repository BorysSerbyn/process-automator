package engine

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/installs"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Template updates: what I installed from a template is recorded
// (installs), so when the app's templates change (an update ships new
// ones) each install whose template's hash changed is offered, and
// overriding it replaces my copy with the new template, filled in with my
// parameter values, keeping its name, settings, memories and trust.

// Whether an install's copy was edited since it was installed.
const (
	EditedNo      = "no"
	EditedYes     = "yes"
	EditedUnknown = "unknown"
)

// TemplateUpdate is one install whose template changed: one row of
// `templates outdated --json`.
type TemplateUpdate struct {
	// Name is what it's installed as, Kind what it is.
	Name string `json:"name"`
	Kind string `json:"kind" jsonschema:"enum=routine,enum=program,enum=hook,enum=watcher"`
	// Template is the template's ID; InstalledVersion the version it was
	// installed at and Version the template's now ("" for none), Changes
	// what the template says changed.
	Template         string `json:"template"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	Version          string `json:"version,omitempty"`
	Changes          string `json:"changes,omitempty"`
	// Edited is whether my copy changed since it was installed: no, yes, or
	// unknown (installed before installs were recorded, and not the
	// template's content then); overriding it replaces my edits.
	Edited string `json:"edited" jsonschema:"enum=no,enum=yes,enum=unknown"`
	// Skipped is that I chose not to take this change (listed with --all).
	Skipped bool `json:"skipped,omitempty"`
	// Blocked says why it can't be overridden ("" when it can): installing
	// the template again is the way then.
	Blocked string `json:"blocked,omitempty"`
	// With are installs overridden with it: a hook's watcher whose
	// template changed too.
	With []string `json:"with,omitempty"`
}

// Overridden is one install a template update replaced.
type Overridden struct {
	Name    string `json:"name"`
	Kind    string `json:"kind" jsonschema:"enum=routine,enum=program,enum=hook,enum=watcher"`
	Version string `json:"version,omitempty"`
	// Trusted is whether it's trusted now: kept when it was before.
	Trusted bool `json:"trusted"`
}

// installStore is the record of installs.
func (a *App) installStore() installs.Store {
	return installs.Store{Path: a.Layout.TemplateInstalls()}
}

// RecordInstall records name as installed from t, with the parameter
// values filled into it (a folder template's).
func (a *App) RecordInstall(name string, t templates.Template, values map[string]string) error {
	h, err := a.installedHash(t.Kind, name)
	if err != nil {
		return err
	}
	rec := installs.Record{Template: t.ID, Kind: t.Kind, Version: t.Version, Hash: t.Hash, Installed: h}
	if t.Kind != templates.KindRoutine && len(t.Params) > 0 {
		rec.Values = map[string]string{}
		for _, p := range t.Params {
			if v, ok := values[p.Name]; ok {
				rec.Values[p.Name] = v
			}
		}
	}
	return a.installStore().Set(name, rec)
}

// ForgetInstall forgets name's install record: it was deleted.
func (a *App) ForgetInstall(name string) error { return a.installStore().Forget(name) }

// installed reports whether one of kind named name is there.
func (a *App) installed(kind, name string) bool {
	switch kind {
	case templates.KindRoutine:
		_, err := os.Stat(filepath.Join(a.Layout.Routines(), name+routines.Ext))
		return err == nil
	case templates.KindProgram:
		return programs.Exists(a.Layout.Programs(), name)
	case templates.KindHook:
		return hooks.Exists(a.Layout.Hooks(), name)
	case templates.KindWatcher:
		return watchers.Exists(a.Layout.Watchers(), name)
	}
	return false
}

// installedHash is the hash of what's installed as name: a routine's file,
// or every file in a program's, hook's or watcher's folder.
func (a *App) installedHash(kind, name string) (string, error) {
	switch kind {
	case templates.KindRoutine:
		return templates.FileHash(filepath.Join(a.Layout.Routines(), name+routines.Ext))
	case templates.KindProgram:
		return dirHash(filepath.Join(a.Layout.Programs(), name))
	case templates.KindHook:
		return dirHash(filepath.Join(a.Layout.Hooks(), name))
	case templates.KindWatcher:
		return dirHash(filepath.Join(a.Layout.Watchers(), name))
	}
	return "", fmt.Errorf("%q isn't a kind of template", kind)
}

// dirHash hashes the files in dir (hidden ones aside), each by its path in
// it and content.
func dirHash(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && path != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		h, err := templates.FileHash(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		lines = append(lines, filepath.ToSlash(rel)+" "+h)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	return templates.TextHash(strings.Join(lines, "\n")), nil
}

// shipped is the templates this app ships, for this OS.
func shipped() ([]templates.Template, error) {
	dir, err := TemplatesDir()
	if err != nil {
		return nil, err
	}
	list, err := templates.List(dir)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(list, func(t templates.Template) bool { return !t.RunsHere() }), nil
}

// installRecords is the record of installs, with those installed before
// installs were recorded matched to their templates (matched: their names,
// not yet written down).
func (a *App) installRecords(list []templates.Template) (f *installs.File, matched []string, err error) {
	if f, err = a.installStore().Load(); err != nil {
		return nil, nil, err
	}
	byName := map[string][]templates.Template{}
	for _, t := range list {
		byName[t.Name] = append(byName[t.Name], t)
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := f.Installs[name]; ok {
			continue
		}
		if rec, ok := a.match(name, byName[name]); ok {
			f.Installs[name] = rec
			matched = append(matched, name)
		}
	}
	return f, matched, nil
}

// match matches what's installed as name, before installs were recorded,
// to one of the templates of that name: the one of its kind whose content
// it is (recorded as installed from it as it is now), else the only one of
// its kind (or, for a routine, the only one whose parameters are the ones
// it has values for), recorded as possibly edited.
func (a *App) match(name string, cands []templates.Template) (installs.Record, bool) {
	var kind []templates.Template
	for _, t := range cands {
		if a.installed(t.Kind, name) {
			kind = append(kind, t)
		}
	}
	if len(kind) == 0 {
		return installs.Record{}, false
	}
	cur, err := a.installedHash(kind[0].Kind, name)
	if err != nil {
		return installs.Record{}, false
	}
	for _, t := range kind {
		if same, err := a.sameAsTemplate(t, name, cur); err == nil && same {
			return installs.Record{Template: t.ID, Kind: t.Kind, Version: t.Version, Hash: t.Hash, Installed: cur, Matched: true}, true
		}
	}
	if len(kind) > 1 && kind[0].Kind == templates.KindRoutine {
		kind = slices.DeleteFunc(slices.Clone(kind), func(t templates.Template) bool { return !a.paramsSet(name, t) })
	}
	if len(kind) > 1 { // a project's own is the more specific
		if mine := slices.DeleteFunc(slices.Clone(kind), func(t templates.Template) bool { return t.Project == "" }); len(mine) == 1 {
			kind = mine
		}
	}
	if len(kind) != 1 {
		return installs.Record{}, false
	}
	t := kind[0]
	return installs.Record{Template: t.ID, Kind: t.Kind, Matched: true}, true
}

// sameAsTemplate reports whether what's installed as name (its hash cur)
// is what installing t with its defaults writes (a routine's version and
// changes lines aside). A template that's built
// can't be told: its executable isn't built to compare.
func (a *App) sameAsTemplate(t templates.Template, name, cur string) (bool, error) {
	if t.Kind == templates.KindRoutine {
		return cur == t.Hash || a.sameButVersion(t, name), nil
	}
	if t.Build != nil {
		return false, nil
	}
	tmp, err := os.MkdirTemp("", "template-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	desc, err := t.Stage(context.Background(), nil, tmp, io.Discard)
	if err != nil {
		return false, err
	}
	staged, err := dirHash(filepath.Dir(desc))
	return err == nil && staged == cur, err
}

// sameButVersion reports whether the routine name is t's file but for its
// version and changes lines: the template as it was before it said them,
// which don't change how it runs.
func (a *App) sameButVersion(t templates.Template, name string) bool {
	mine, err := os.ReadFile(filepath.Join(a.Layout.Routines(), name+routines.Ext))
	if err != nil {
		return false
	}
	return withoutVersion(string(mine)) == withoutVersion(t.Routine.Source)
}

// withoutVersion is a routine file without its automator.version and
// automator.changes lines.
func withoutVersion(src string) string {
	lines := strings.Split(src, "\n")
	return strings.Join(slices.DeleteFunc(lines, func(l string) bool {
		return strings.HasPrefix(l, routines.Namespace+".version:") || strings.HasPrefix(l, routines.Namespace+".changes:")
	}), "\n")
}

// paramsSet reports whether the routine name has a value for each of t's
// parameters and for no other.
func (a *App) paramsSet(name string, t templates.Template) bool {
	set := 0
	for _, p := range t.Params {
		st := settings.Store{Path: a.Layout.Config(), Extra: []settings.Setting{p.Setting(name)}}
		if s, err := st.Get(p.Setting(name).Key); err == nil && !s.IsDefault {
			set++
		}
	}
	prefix := "routines." + name + ".params."
	all, err := (settings.Store{Path: a.Layout.Config()}).RawKeys()
	if err != nil {
		return false
	}
	have := 0
	for _, k := range all {
		if strings.HasPrefix(k, prefix) {
			have++
		}
	}
	return set == len(t.Params) && have == set
}

// RecordMatchedInstalls writes down the installs made before installs were
// recorded, matched to their templates, so a later template is compared
// with the one they were matched to. The scheduler does it as it starts.
func (a *App) RecordMatchedInstalls() error {
	list, err := shipped()
	if err != nil {
		return err
	}
	f, matched, err := a.installRecords(list)
	if err != nil || len(matched) == 0 {
		return err
	}
	return a.installStore().Change(func(cur *installs.File) error {
		for _, n := range matched {
			if _, ok := cur.Installs[n]; !ok {
				cur.Installs[n] = f.Installs[n]
			}
		}
		return nil
	})
}

// TemplateUpdates lists the installs whose template changed since they
// were installed, by name; all: those I chose not to take too.
func (a *App) TemplateUpdates(all bool) ([]TemplateUpdate, error) {
	list, err := shipped()
	if err != nil {
		return nil, err
	}
	f, _, err := a.installRecords(list)
	if err != nil {
		return nil, err
	}
	byID := map[string]templates.Template{}
	for _, t := range list {
		byID[t.ID] = t
	}
	backend, err := a.BackendDef()
	if err != nil {
		return nil, err
	}
	out := []TemplateUpdate{}
	for name, rec := range f.Installs {
		t, ok := byID[rec.Template]
		if !ok || t.Kind != rec.Kind || t.Hash == rec.Hash || !a.installed(rec.Kind, name) {
			continue
		}
		skipped := rec.Skipped == t.Hash
		if skipped && !all {
			continue
		}
		u := TemplateUpdate{Name: name, Kind: rec.Kind, Template: t.ID, InstalledVersion: rec.Version, Version: t.Version,
			Changes: t.Changes, Edited: EditedUnknown, Skipped: skipped}
		if rec.Installed != "" {
			u.Edited = EditedNo
			if cur, err := a.installedHash(rec.Kind, name); err != nil || cur != rec.Installed {
				u.Edited = EditedYes
			}
		}
		u.Blocked = a.blocked(name, t, rec, backend.Name)
		if t.Kind == templates.KindHook {
			if w, ok := f.Installs[t.Watcher]; ok {
				if wt, ok := byID[w.Template]; ok && wt.Hash != w.Hash && a.installed(templates.KindWatcher, t.Watcher) {
					u.With = []string{t.Watcher}
				}
			}
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// blocked says why name can't be overridden by t ("" when it can).
func (a *App) blocked(name string, t templates.Template, rec installs.Record, backend string) string {
	if !t.Supports(backend) {
		return fmt.Sprintf("the new version works only with the %s backend", strings.Join(t.Backends, " or "))
	}
	var missing []string
	for _, p := range t.Params {
		if !p.Required {
			continue
		}
		if t.Kind == templates.KindRoutine {
			st := settings.Store{Path: a.Layout.Config(), Extra: []settings.Setting{p.Setting(name)}}
			if s, err := st.Get(p.Setting(name).Key); err == nil && !s.IsDefault {
				continue
			}
		} else if _, ok := rec.Values[p.Name]; ok {
			continue
		}
		missing = append(missing, p.Name)
	}
	if len(missing) > 0 {
		return "the new version needs a value for " + strings.Join(missing, ", ") + ": install it again"
	}
	return ""
}

// SkipTemplateUpdates records that I'm not taking the template changes
// offered for names: they aren't offered again, a later change is.
func (a *App) SkipTemplateUpdates(names []string) error {
	ups, err := a.TemplateUpdates(true)
	if err != nil {
		return err
	}
	list, err := shipped()
	if err != nil {
		return err
	}
	f, _, err := a.installRecords(list)
	if err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, n := range names {
		i := slices.IndexFunc(ups, func(u TemplateUpdate) bool { return u.Name == n })
		if i < 0 {
			return fmt.Errorf("%s has no template update", n)
		}
		t, err := findShipped(list, ups[i].Template)
		if err != nil {
			return err
		}
		hashes[n] = t.Hash
	}
	return a.installStore().Change(func(cur *installs.File) error {
		for n, h := range hashes {
			rec, ok := cur.Installs[n]
			if !ok {
				rec = f.Installs[n]
			}
			rec.Skipped = h
			cur.Installs[n] = rec
		}
		return nil
	})
}

func findShipped(list []templates.Template, id string) (templates.Template, error) {
	for _, t := range list {
		if t.ID == id {
			return t, nil
		}
	}
	return templates.Template{}, fmt.Errorf("no template %s", id)
}

// OverrideFromTemplates replaces each of names with its template's new
// version (and a hook's watcher whose template changed too, first),
// keeping its name, settings and memories, and its trust when it was
// trusted. A build's output goes to log. It stops at the first that fails,
// returning those done.
func (a *App) OverrideFromTemplates(ctx context.Context, names []string, log io.Writer) ([]Overridden, error) {
	ups, err := a.TemplateUpdates(true)
	if err != nil {
		return nil, err
	}
	list, err := shipped()
	if err != nil {
		return nil, err
	}
	f, _, err := a.installRecords(list)
	if err != nil {
		return nil, err
	}
	var order []string
	for _, n := range names {
		i := slices.IndexFunc(ups, func(u TemplateUpdate) bool { return u.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("%s has no template update", n)
		}
		if ups[i].Blocked != "" {
			return nil, fmt.Errorf("%s: %s", n, ups[i].Blocked)
		}
		for _, w := range ups[i].With {
			if !slices.Contains(order, w) {
				order = append(order, w)
			}
		}
		if !slices.Contains(order, n) {
			order = append(order, n)
		}
	}
	var done []Overridden
	for _, n := range order {
		rec := f.Installs[n]
		t, err := findShipped(list, rec.Template)
		if err != nil {
			return done, err
		}
		o, err := a.override(ctx, n, t, rec, log)
		if err != nil {
			return done, fmt.Errorf("overriding %s with %s: %w", n, t.ID, err)
		}
		done = append(done, o)
	}
	return done, nil
}

// override replaces name with t, filled in with rec's values.
func (a *App) override(ctx context.Context, name string, t templates.Template, rec installs.Record, log io.Writer) (Overridden, error) {
	trusted, err := a.trustedNow(t.Kind, name)
	if err != nil {
		return Overridden{}, err
	}
	switch t.Kind {
	case templates.KindRoutine:
		src, err := os.ReadFile(t.Path)
		if err != nil {
			return Overridden{}, err
		}
		if _, err := routines.Update(a.Layout.Routines(), name, string(src)); err != nil {
			return Overridden{}, err
		}
	default:
		tmp, err := os.MkdirTemp("", "template-")
		if err != nil {
			return Overridden{}, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		desc, err := t.Stage(ctx, rec.Values, tmp, log)
		if err != nil {
			return Overridden{}, err
		}
		switch t.Kind {
		case templates.KindProgram:
			_, err = a.AddProgram(name, desc, true)
		case templates.KindHook:
			_, err = a.AddHook(name, desc, true)
		case templates.KindWatcher:
			_, err = a.AddWatcher(name, desc, true)
		}
		if err != nil {
			return Overridden{}, err
		}
	}
	if trusted {
		if err := a.trustNow(t.Kind, name); err != nil {
			return Overridden{}, fmt.Errorf("it's replaced, but trusting it again failed: %w", err)
		}
	}
	h, err := a.installedHash(t.Kind, name)
	if err != nil {
		return Overridden{}, err
	}
	nrec := installs.Record{Template: t.ID, Kind: t.Kind, Version: t.Version, Hash: t.Hash, Installed: h, Values: rec.Values}
	if err := a.installStore().Set(name, nrec); err != nil {
		return Overridden{}, err
	}
	return Overridden{Name: name, Kind: t.Kind, Version: t.Version, Trusted: trusted}, nil
}

// trustedNow reports whether what's installed as name is trusted as it is.
func (a *App) trustedNow(kind, name string) (bool, error) {
	if kind == templates.KindWatcher {
		w, err := a.FindWatcher(name)
		if err != nil {
			return false, err
		}
		why, err := a.WatcherUntrusted(w)
		return why == "", err
	}
	x, err := a.FindAny(name)
	if err != nil {
		return false, err
	}
	why, _, err := a.TrustProblem(x)
	return why == "", err
}

// trustNow trusts what's installed as name, as it is now.
func (a *App) trustNow(kind, name string) error {
	if kind == templates.KindWatcher {
		w, err := a.FindWatcher(name)
		if err != nil {
			return err
		}
		return a.TrustWatcher(w)
	}
	x, err := a.FindAny(name)
	if err != nil {
		return err
	}
	return a.Trust(x)
}
