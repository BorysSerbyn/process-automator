// Package routines loads routines from the user's device. The binary ships none.
//
// The routines folder follows Dotprompt's directory conventions:
//
//	<name>.prompt    a routine (the file name is the routine name)
//	_<name>.prompt   a partial, usable from any routine as {{> name}}
//
// Routine settings are Dotprompt extension fields in the "automator" namespace
// (automator.servers, automator.schedule, automator.staleness); everything else in the
// frontmatter (model, config, tools, ...) is plain Dotprompt.
package routines

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/dotprompt/go/dotprompt"

	"go.yaml.in/yaml/v3"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

const (
	// Ext is the routine and partial file extension.
	Ext = ".prompt"
	// Namespace holds the app's frontmatter extension fields (automator.*).
	Namespace = "automator"
	// partialPrefix marks partial files, as in Dotprompt's directory store.
	partialPrefix = "_"
	// MemoriesPartial is provided by the app; a partial file may not use the name.
	MemoriesPartial = "memories"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Routine is one routine file.
type Routine struct {
	Name    string
	Path    string
	Servers []string
	Tools   []string
	// Model is the frontmatter's model as written (e.g. "anthropic/claude-sonnet-5-5"),
	// empty for the backend's default.
	Model string
	// Description is the frontmatter's description (Dotprompt's own field).
	Description string
	// File holds the routine settings its file sets (automator.<field>; see
	// settings.RoutineFields), as written. Effective values come from settings.
	File map[string]any
	// Params are the routine's parameters (automator.parameters): inputs whose
	// values are per-routine settings (routines.<name>.params.<param>).
	Params []Param
	// Platforms it runs on (automator.platforms: macos, linux); empty means all.
	Platforms []string
	// Backends it works with (automator.backends: llm.backend's values);
	// empty means any.
	Backends []string
	// Plugins holds the routine's section for each in-process plugin it uses
	// (automator.plugins.<name>), as written; the plugin decodes its own.
	Plugins map[string]any
	Source  string
}

// Param is one routine parameter. Its value is a per-routine setting; a
// parameter without a default is required.
type Param struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	// Required is true for a parameter without a default; DefaultText is the
	// default written as `templates install --set` takes it. Both are worked
	// out when the routine is parsed.
	Required    bool   `json:"required"`
	DefaultText string `json:"defaultText,omitempty"`
}

// paramInput is a parameter as a routine file writes it (without Param's
// worked-out fields, so a file can't set them).
type paramInput struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Setting is the parameter as a per-routine setting.
func (p Param) Setting(routine string) settings.Setting {
	return settings.Setting{
		Key: "routines." + routine + ".params." + p.Name, Type: settings.Type(p.Type),
		Default: p.Default, Description: p.Description, Enum: p.Enum,
	}
}

// Platforms routines can name; Platform is the one this process runs on.
var knownPlatforms = map[string]bool{"macos": true, "linux": true}

// Platform is this OS as routines name it.
func Platform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

// RunsHere reports whether the routine supports this OS.
func (r Routine) RunsHere() bool {
	return len(r.Platforms) == 0 || slices.Contains(r.Platforms, Platform())
}

// Supports reports whether the routine works with backend (an llm.backend
// value).
func (r Routine) Supports(backend string) bool {
	return len(r.Backends) == 0 || slices.Contains(r.Backends, backend)
}

// Settings are the routine's per-routine settings, with defaults from its
// file: the declared fields, then one per parameter.
func (r Routine) Settings() []settings.Setting {
	out := settings.RoutineSettings(r.Name, r.File)
	for _, p := range r.Params {
		out = append(out, p.Setting(r.Name))
	}
	return out
}

// Scheduled reports whether the routine's file gives it a schedule.
func (r Routine) Scheduled() bool { _, ok := r.File["schedule"]; return ok }

// FileError is a problem with one file. Other files still load.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }
func (e *FileError) Unwrap() error { return e.Err }

// Set is the content of a routines folder.
type Set struct {
	Routines []Routine
	// Partials are shared snippets, by name.
	Partials map[string]string
	// Errors lists files that couldn't be loaded; they're skipped.
	Errors []*FileError
}

// Dir is the routines folder inside the app's data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "routines") }

// Load reads a routines folder. Each file loads independently: a broken file is
// reported in Set.Errors and skipped, never taking the other routines down.
// A missing folder is an empty set.
func Load(dir string) (*Set, error) {
	set := &Set{Partials: map[string]string{}}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		file := e.Name()
		if e.IsDir() || strings.HasPrefix(file, ".") || !strings.HasSuffix(file, Ext) {
			continue
		}
		path := filepath.Join(dir, file)
		stem := strings.TrimSuffix(file, Ext)
		fail := func(err error) { set.Errors = append(set.Errors, &FileError{Path: path, Err: err}) }

		if name, ok := strings.CutPrefix(stem, partialPrefix); ok {
			src, err := loadPartial(path, name)
			if err != nil {
				fail(err)
				continue
			}
			set.Partials[name] = src
			continue
		}
		r, err := parse(path, stem)
		if err != nil {
			fail(err)
			continue
		}
		set.Routines = append(set.Routines, r)
	}
	sort.Slice(set.Routines, func(i, j int) bool { return set.Routines[i].Name < set.Routines[j].Name })
	return set, nil
}

// ValidName reports whether name can name a routine (its file name).
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Find returns the named routine from the set.
func (s *Set) Find(name string) (Routine, error) {
	for _, r := range s.Routines {
		if r.Name == name {
			return r, nil
		}
	}
	for _, e := range s.Errors {
		if filepath.Base(e.Path) == name+Ext {
			return Routine{}, e
		}
	}
	names := make([]string, 0, len(s.Routines))
	for _, r := range s.Routines {
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		return Routine{}, fmt.Errorf("no routine %q: there are no routines installed", name)
	}
	return Routine{}, fmt.Errorf("no routine %q (have: %s)", name, strings.Join(names, ", "))
}

func loadPartial(path, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", errors.New("partial names are lowercase letters, digits and dashes")
	}
	if name == MemoriesPartial {
		return "", fmt.Errorf("the %q partial is provided by the app", MemoriesPartial)
	}
	b, err := os.ReadFile(path)
	return string(b), err
}

func parse(path, name string) (Routine, error) {
	if !nameRE.MatchString(name) {
		return Routine{}, errors.New("routine names are lowercase letters, digits and dashes (variants like name.variant.prompt aren't supported)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Routine{}, err
	}
	return parseSource(path, name, raw)
}

func parseSource(path, name string, raw []byte) (Routine, error) {
	if err := checkFrontmatter(raw); err != nil {
		return Routine{}, err
	}
	doc, err := dotprompt.ParseDocument(string(raw))
	if err != nil {
		return Routine{}, err
	}
	if _, nested := doc.Raw[Namespace]; nested {
		return Routine{}, fmt.Errorf("write %s fields as dotted keys (%s.servers: [...]), not a nested %s: block", Namespace, Namespace, Namespace)
	}
	ext := doc.Ext[Namespace]
	// Plugin sections can be written dotted (automator.plugins.scripts: ...),
	// which Dotprompt files under the namespace "automator.plugins", or nested
	// under automator.plugins; any other deeper automator.* key is unknown.
	plugins := map[string]any{}
	for ns, entries := range doc.Ext {
		switch {
		case ns == Namespace:
		case ns == Namespace+".plugins":
			for name, section := range entries {
				plugins[name] = section
			}
		case strings.HasPrefix(ns, Namespace+"."):
			for field := range entries {
				return Routine{}, fmt.Errorf("%s.* fields: unknown field %q", Namespace, strings.TrimPrefix(ns, Namespace+".")+"."+field)
			}
		}
	}
	fields := map[string]bool{"servers": true, "parameters": true, "platforms": true, "backends": true, "plugins": true}
	for _, n := range settings.RoutineFieldNames() {
		fields[n] = true
	}
	file := map[string]any{}
	var servers, platforms, backends []string
	var params []Param
	for k, v := range ext {
		switch {
		case k == "servers":
			if err := decodeStrict(map[string]any{"servers": v}, &struct {
				Servers *[]string `json:"servers"`
			}{&servers}); err != nil {
				return Routine{}, fmt.Errorf("%s.servers: %w", Namespace, err)
			}
		case k == "parameters":
			var written []paramInput
			if err := decodeStrict(map[string]any{"p": v}, &struct {
				P *[]paramInput `json:"p"`
			}{&written}); err != nil {
				return Routine{}, fmt.Errorf("%s.parameters: %w", Namespace, err)
			}
			for _, in := range written {
				params = append(params, Param{Name: in.Name, Type: in.Type, Default: in.Default, Description: in.Description, Enum: in.Enum})
			}
		case k == "plugins":
			m, ok := v.(map[string]any)
			if !ok {
				return Routine{}, fmt.Errorf("%s.plugins: want a section per plugin (%s.plugins.<name>: ...)", Namespace, Namespace)
			}
			for name, section := range m {
				if _, dup := plugins[name]; dup {
					return Routine{}, fmt.Errorf("%s.plugins.%s is written twice", Namespace, name)
				}
				plugins[name] = section
			}
		case k == "platforms":
			if err := decodeStrict(map[string]any{"p": v}, &struct {
				P *[]string `json:"p"`
			}{&platforms}); err != nil {
				return Routine{}, fmt.Errorf("%s.platforms: %w", Namespace, err)
			}
		case k == "backends":
			if err := decodeStrict(map[string]any{"b": v}, &struct {
				B *[]string `json:"b"`
			}{&backends}); err != nil {
				return Routine{}, fmt.Errorf("%s.backends: %w", Namespace, err)
			}
		case fields[k]:
			file[k] = v
		default:
			return Routine{}, fmt.Errorf("%s.* fields: unknown field %q", Namespace, k)
		}
	}
	if len(servers) == 0 && len(doc.Tools) > 0 {
		return Routine{}, fmt.Errorf("lists tools but no %s.servers", Namespace)
	}
	r := Routine{
		Name: name, Path: path, Servers: servers, Tools: doc.Tools, Model: doc.Model, Description: doc.Description,
		File: file, Params: params, Platforms: platforms, Backends: backends, Source: string(raw),
	}
	if len(plugins) > 0 {
		r.Plugins = plugins
	}
	if err := checkParams(params); err != nil {
		return Routine{}, err
	}
	for i, p := range params {
		params[i].Required = p.Default == nil
		if p.Default != nil {
			params[i].DefaultText = p.Setting(name).Text(p.Default)
		}
	}
	for name := range plugins {
		if !slices.Contains(servers, name) {
			return Routine{}, fmt.Errorf("%s.plugins.%s: %s isn't one of %s.servers", Namespace, name, name, Namespace)
		}
	}
	for _, p := range platforms {
		if !knownPlatforms[p] {
			return Routine{}, fmt.Errorf("%s.platforms: unknown platform %q (want macos or linux)", Namespace, p)
		}
	}
	if def, ok := settings.Lookup("llm.backend"); ok {
		for _, b := range backends {
			if !slices.Contains(def.Enum, b) {
				return Routine{}, fmt.Errorf("%s.backends: unknown backend %q (want %s)", Namespace, b, strings.Join(def.Enum, " or "))
			}
		}
	}
	if _, ok := file["staleness"]; ok && !r.Scheduled() {
		return Routine{}, fmt.Errorf("%s.staleness needs %s.schedule", Namespace, Namespace)
	}
	// Validate every field with the same rules `settings set` uses.
	for _, def := range r.Settings() {
		if def.Default == nil {
			continue
		}
		if _, err := def.Normalize(def.Default); err != nil {
			return Routine{}, err
		}
	}
	return r, nil
}

// inputNames are the inputs every routine gets; parameters can't reuse them.
var inputNames = map[string]bool{"today": true, "since": true, "now": true, "trigger": true}

func checkParams(params []Param) error {
	seen := map[string]bool{}
	for _, p := range params {
		switch {
		case !paramRE.MatchString(p.Name):
			return fmt.Errorf("%s.parameters: %q: names are lowercase letters, digits and underscores, starting with a letter", Namespace, p.Name)
		case inputNames[p.Name]:
			return fmt.Errorf("%s.parameters: %q is a built-in input", Namespace, p.Name)
		case seen[p.Name]:
			return fmt.Errorf("%s.parameters: %q declared twice", Namespace, p.Name)
		}
		seen[p.Name] = true
		def := p.Setting("x")
		if !def.Type.Valid() {
			return fmt.Errorf("%s.parameters: %q: unknown type %q", Namespace, p.Name, p.Type)
		}
		if p.Type == string(settings.TypeEnum) && len(p.Enum) == 0 {
			return fmt.Errorf("%s.parameters: %q: an enum needs its values (enum: [...])", Namespace, p.Name)
		}
		if p.Default != nil {
			if _, err := def.Normalize(p.Default); err != nil {
				return fmt.Errorf("%s.parameters: %q: default: %w", Namespace, p.Name, err)
			}
		}
	}
	return nil
}

// paramRE: parameter names are template-friendly ({{lookback}}).
var paramRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// decodeStrict maps the extension fields onto v, rejecting unknown keys (typos).
func decodeStrict(m map[string]any, v any) error {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Inputs are the values every routine's prompt can use, for the occurrence
// it runs for: today, since and now are as of its time (a backfilled run sees
// the day it's for), and trigger is what fired ({{trigger.kind}}: schedule,
// manual or backfill; id, at, and data when the trigger has any).
func Inputs(o trigger.Occurrence) map[string]any {
	return map[string]any{
		"today":   o.At.Format("Monday 2006-01-02"),
		"since":   PreviousWorkday(o.At).Format("Monday 2006-01-02"),
		"now":     o.At.Format(time.RFC3339),
		"trigger": o.Input(),
	}
}

// PreviousWorkday is the start (local midnight) of the last weekday before now's
// day: on Monday that's Friday.
func PreviousWorkday(now time.Time) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for {
		d = d.AddDate(0, 0, -1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			return d
		}
	}
}

// PromptPartials are the partials a routine renders with: the folder's
// partials plus the app's memories (empty until memories exist).
func (s *Set) PromptPartials(memories string) map[string]string {
	out := map[string]string{MemoriesPartial: memories}
	for name, src := range s.Partials {
		out[name] = src
	}
	return out
}

// Parse validates source as the routine name, the same way a routine file is
// loaded, without writing anything.
func Parse(name, source string) (Routine, error) {
	if !nameRE.MatchString(name) {
		return Routine{}, fmt.Errorf("%q can't name a routine: lowercase letters, digits and dashes", name)
	}
	if strings.TrimSpace(source) == "" {
		return Routine{}, errors.New("the routine file is empty")
	}
	return parseSource(name+Ext, name, []byte(source))
}

// Create validates source and writes it as a new routine in dir; it never
// replaces an existing file.
func Create(dir, name, source string) (Routine, error) {
	if _, err := Parse(name, source); err != nil {
		return Routine{}, err
	}
	path := filepath.Join(dir, name+Ext)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Routine{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return Routine{}, fmt.Errorf("routine %q already exists (%s); update it instead", name, path)
	}
	if err != nil {
		return Routine{}, err
	}
	if _, err := f.WriteString(source); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return Routine{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return Routine{}, err
	}
	return parse(path, name)
}

// Update validates source and atomically replaces an existing routine's file.
func Update(dir, name, source string) (Routine, error) {
	if _, err := Parse(name, source); err != nil {
		return Routine{}, err
	}
	path := filepath.Join(dir, name+Ext)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Routine{}, fmt.Errorf("no routine %q to update; create it instead", name)
		}
		return Routine{}, err
	}
	// The temp file is hidden, so it's never loaded as a routine.
	if err := atomicfile.Write(path, []byte(source)); err != nil {
		return Routine{}, err
	}
	return parse(path, name)
}

// Delete removes a routine's file (a broken one too) and returns its path.
func Delete(dir, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("%q isn't a routine name", name)
	}
	path := filepath.Join(dir, name+Ext)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("no routine %q", name)
		}
		return "", err
	}
	return path, nil
}

// checkFrontmatter rejects frontmatter that isn't valid YAML. Dotprompt
// would print the error to stdout and go on as if there were none, losing the
// routine's settings and allowlist.
func checkFrontmatter(raw []byte) error {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return errors.New("frontmatter starts with --- but has no closing --- line")
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s[4:4+end]), &m); err != nil {
		return fmt.Errorf("frontmatter isn't valid YAML: %w", err)
	}
	return nil
}
