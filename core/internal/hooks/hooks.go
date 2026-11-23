// Package hooks keeps hooks on the user's device: a prompt or a program run
// when something happens, instead of on a schedule. What happens comes from a
// watcher (package watchers); a hook says which watcher it listens to and for
// which type of event (on), and what it does: its action. Each hook is a
// folder of its own in the hooks folder:
//
//	<name>/hook.prompt    an LLM hook: a Dotprompt file, as a routine's
//
// or
//
//	<name>/hook.yaml      a program hook: its description file
//	<name>/<executable>   the executable it names, copied
//
// A hook has no schedule, and a routine has no trigger but its schedule:
// they're kept apart.
package hooks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
)

// A hook's file, in its folder: its prompt, or its program's description.
const (
	PromptFile      = "hook.prompt"
	DescriptionFile = "hook.yaml"
)

// On is what a hook listens for: a watcher's events, of one type or (no
// type) every type.
type On struct {
	Watcher string `yaml:"watcher" json:"watcher"`
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	// Label is what its type means, as its watcher declares it ("End key
	// pressed"): set by the app from the watcher, never written in a hook's
	// file. Absent when it has no type, or its watcher declares none.
	Label string `yaml:"-" json:"label,omitempty"`
}

// Matches reports whether an event of type t is one the hook listens for.
func (o On) Matches(t string) bool { return o.Type == "" || o.Type == t }

// String is it in words: "End key pressed (from nav-keys)", or without a
// label "a key.end event (from nav-keys)", or without a type "any event
// (from nav-keys)".
func (o On) String() string {
	switch {
	case o.Type == "":
		return "any event (from " + o.Watcher + ")"
	case o.Label == "":
		return "a " + o.Type + " event (from " + o.Watcher + ")"
	}
	return o.Label + " (from " + o.Watcher + ")"
}

// When is when the hook runs: "When End key pressed (from nav-keys)".
func (o On) When() string { return "When " + o.String() }

func (o On) check(file string) error {
	switch {
	case o.Watcher == "":
		return fmt.Errorf("%s: on.watcher is required: the watcher whose events it acts on", file)
	case !watchers.ValidName(o.Watcher):
		return fmt.Errorf("%s: on.watcher %q isn't a watcher's name", file, o.Watcher)
	case o.Type != "" && !watchers.ValidType(o.Type):
		return fmt.Errorf("%s: on.type %q: letters, digits and . _ : / -, like meeting.started", file, o.Type)
	}
	return nil
}

// Hook is one hook, as the app keeps it. Exactly one of Prompt and Program
// is set: its action.
type Hook struct {
	Name        string
	Dir, Path   string
	Description string
	On          On
	// Batch (an LLM hook's) gives a run every event that came while it
	// waited for its turn, instead of one run per event.
	Batch bool
	// Prompt is an LLM hook's prompt, as a routine (Prompt.Hook is set).
	Prompt *routines.Routine
	// Program is a program hook's program (Program.Hook is set).
	Program *programs.Program
	Source  string
}

// Settings are its settings (hooks.<name>.<field>), with defaults from its
// file.
func (h Hook) Settings() []settings.Setting {
	if h.Prompt != nil {
		return h.Prompt.Settings()
	}
	return h.Program.Settings()
}

// Key is the setting key of one of its fields.
func (h Hook) Key(field string) string { return "hooks." + h.Name + "." + field }

// Fields are a program hook's hook.yaml keys, for the guide and the docs.
var Fields = []programs.Field{
	{Name: "executable", Type: "path", Description: "Required. The executable, relative to this file's folder (or absolute). It's copied when the hook is added."},
	{Name: "writes", Type: "bool", Description: "Required. Whether it can change anything. A dry run passes --dry-run to one that writes, and runs one that doesn't as it is."},
	{Name: "on", Type: "watcher, type", Description: "Required. What it listens for: on.watcher, a watcher's name, and on.type, one of the event types that watcher declares (left out, every type)."},
	{Name: "description", Type: "string", Description: "What it does, in a line."},
	{Name: "name", Type: "string", Description: "Its name; when given, it must be the name it's added as."},
	{Name: "args", Type: "list", Description: "Arguments passed on every run."},
	{Name: "staleness", Type: "duration", Description: "How old an event may be when it would run; older ones are dropped (default 10m)."},
	{Name: "timeout", Type: "duration", Description: "How long a run may take before it's stopped (default 20m)."},
	{Name: "rate", Type: "int", Description: "At most this many runs an hour (default 60); far past it, it's paused."},
	{Name: "secrets", Type: "list", Description: "Secrets it's given as environment variables: name, and optionally env and prompt."},
	programs.PermissionsField,
}

// PromptKeys are an LLM hook's own automator.* keys, besides a routine's, for
// the guide and the docs.
var PromptKeys = []programs.Field{
	{Name: "automator.on", Type: "watcher, type", Description: "Required. What it listens for: watcher, a watcher's name, and type, one of the event types that watcher declares (left out, every type)."},
	{Name: "automator.batch", Type: "bool", Description: "true: one run gets every event that came while it waited for its turn ({{events}}), instead of one run per event."},
}

// file is a program hook's description file as written.
type file struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Executable  string            `yaml:"executable"`
	Args        []string          `yaml:"args"`
	Writes      *bool             `yaml:"writes"`
	On          *On               `yaml:"on"`
	Batch       *bool             `yaml:"batch"`
	Staleness   *string           `yaml:"staleness"`
	Timeout     *string           `yaml:"timeout"`
	Rate        *int              `yaml:"rate"`
	Secrets     []programs.Secret `yaml:"secrets"`
	Permissions []string          `yaml:"permissions"`
}

// Parse validates source as hook name's file, which is a prompt (PromptFile,
// or any .prompt file) or a program's description (otherwise), without
// anything on disk: a program's executable isn't looked at.
func Parse(name, path, source string) (Hook, error) {
	if strings.HasSuffix(path, routines.Ext) {
		return parsePrompt(name, path, source)
	}
	if err := programs.CheckName("a hook", name, source); err != nil {
		return Hook{}, err
	}
	var f file
	if err := yamlfile.DecodeStrict([]byte(source), &f); err != nil {
		return Hook{}, fmt.Errorf("%s: %w", DescriptionFile, err)
	}
	switch {
	case f.Name != "" && f.Name != name:
		return Hook{}, fmt.Errorf("%s names it %q, not %q", DescriptionFile, f.Name, name)
	case f.On == nil:
		return Hook{}, fmt.Errorf("%s: on is required: on: {watcher: <watcher>, type: <event type>}", DescriptionFile)
	case f.Batch != nil:
		return Hook{}, fmt.Errorf("%s: batch is for an LLM hook: a program gets one event per run", DescriptionFile)
	}
	if err := f.On.check(DescriptionFile); err != nil {
		return Hook{}, err
	}
	vals := map[string]any{}
	for k, v := range map[string]*string{"staleness": f.Staleness, "timeout": f.Timeout} {
		if v != nil {
			vals[k] = *v
		}
	}
	if f.Rate != nil {
		vals["rate"] = *f.Rate
	}
	p, err := programs.Build(name, DescriptionFile, programs.Spec{
		Description: f.Description, Executable: f.Executable, Args: f.Args, Writes: f.Writes,
		Secrets: f.Secrets, Permissions: f.Permissions, File: vals, Source: source, Hook: true,
	})
	if err != nil {
		return Hook{}, err
	}
	return Hook{Name: name, Description: f.Description, On: *f.On, Program: &p, Source: source}, nil
}

func parsePrompt(name, path, source string) (Hook, error) {
	r, err := routines.ParseHook(path, name, source)
	if err != nil {
		return Hook{}, err
	}
	h := Hook{Name: name, Description: r.Description, Prompt: &r, Source: source}
	raw, ok := r.HookKeys["on"]
	if !ok {
		return Hook{}, fmt.Errorf("%s.on is required: what it listens for (%s.on: {watcher: <watcher>, type: <event type>})", routines.Namespace, routines.Namespace)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Hook{}, fmt.Errorf("%s.on: want {watcher: <watcher>, type: <event type>}", routines.Namespace)
	}
	for k, v := range m {
		s, ok := v.(string)
		switch {
		case !ok:
			return Hook{}, fmt.Errorf("%s.on.%s: want text", routines.Namespace, k)
		case k == "watcher":
			h.On.Watcher = s
		case k == "type":
			h.On.Type = s
		default:
			return Hook{}, fmt.Errorf("%s.on: unknown field %q (watcher, type)", routines.Namespace, k)
		}
	}
	if err := h.On.check(routines.Namespace + ".on"); err != nil {
		return Hook{}, errors.New(strings.TrimPrefix(err.Error(), routines.Namespace+".on: "))
	}
	if v, ok := r.HookKeys["batch"]; ok {
		b, ok := v.(bool)
		if !ok {
			return Hook{}, fmt.Errorf("%s.batch: want true or false", routines.Namespace)
		}
		h.Batch = b
	}
	return h, nil
}

// load reads the hook in its folder dir.
func load(dir string) (Hook, error) {
	name := filepath.Base(dir)
	for _, f := range []string{PromptFile, DescriptionFile} {
		path := filepath.Join(dir, f)
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Hook{}, err
		}
		h, err := Parse(name, path, string(raw))
		if err != nil {
			return Hook{}, err
		}
		h.Dir, h.Path = dir, path
		if h.Program != nil {
			p, err := programs.Locate(*h.Program, dir, DescriptionFile)
			if err != nil {
				return Hook{}, err
			}
			h.Program = &p
		}
		return h, nil
	}
	return Hook{}, fmt.Errorf("it has neither %s nor %s", PromptFile, DescriptionFile)
}

// FileError is a hook that couldn't be loaded. Others still load.
type FileError struct {
	Name, Path string
	Err        error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }
func (e *FileError) Unwrap() error { return e.Err }

// Set is the content of the hooks folder.
type Set struct {
	Hooks  []Hook
	Errors []*FileError
}

// Dir is the hooks folder inside the app's data folder.
func Dir(dataDir string) string { return filepath.Join(dataDir, "hooks") }

// Load reads the hooks folder: each hook loads independently, and a missing
// folder has none.
func Load(store string) (*Set, error) {
	set := &Set{}
	entries, err := os.ReadDir(store)
	if errors.Is(err, fs.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(store, e.Name())
		h, err := load(dir)
		if err != nil {
			set.Errors = append(set.Errors, &FileError{Name: e.Name(), Path: dir, Err: err})
			continue
		}
		set.Hooks = append(set.Hooks, h)
	}
	sort.Slice(set.Hooks, func(i, j int) bool { return set.Hooks[i].Name < set.Hooks[j].Name })
	return set, nil
}

// Exists reports whether store has a hook, loadable or not, named name.
func Exists(store, name string) bool {
	info, err := os.Stat(filepath.Join(store, name))
	return err == nil && info.IsDir()
}

// Add copies the hook whose file is at from (a .prompt file, or a hook's
// description file with the executable it names beside it) into store as
// name: a new hook, or (replace) in place of the one there. Nothing changes
// if it fails.
func Add(store, name, from string, replace bool) (Hook, error) {
	raw, err := os.ReadFile(from)
	if err != nil {
		return Hook{}, err
	}
	h, err := Parse(name, from, string(raw))
	if err != nil {
		return Hook{}, err
	}
	switch exists := Exists(store, name); {
	case exists && !replace:
		return Hook{}, fmt.Errorf("hook %q already exists (%s); update it instead", name, filepath.Join(store, name))
	case !exists && replace:
		return Hook{}, fmt.Errorf("no hook %q to update; add it instead", name)
	}
	file, exe := PromptFile, ""
	if h.Program != nil {
		file, exe = DescriptionFile, programs.ExecutableBeside(from, h.Source)
	}
	if err := programs.Install(store, name, file, raw, exe, replace); err != nil {
		return Hook{}, err
	}
	return load(filepath.Join(store, name))
}

// Delete removes a hook's folder (a broken one too) and returns its file's
// path.
func Delete(store, name string) (string, error) {
	if !programs.ValidName(name) || !Exists(store, name) {
		return "", fmt.Errorf("no hook %q", name)
	}
	dir := filepath.Join(store, name)
	path := filepath.Join(dir, DescriptionFile)
	if _, err := os.Stat(filepath.Join(dir, PromptFile)); err == nil {
		path = filepath.Join(dir, PromptFile)
	}
	return path, os.RemoveAll(dir)
}
