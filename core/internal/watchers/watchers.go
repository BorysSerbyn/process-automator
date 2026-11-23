// Package watchers keeps watchers on the user's device: programs the daemon
// runs to learn that something happened, for hooks to act on. Each is a
// folder of its own in the watchers folder, the app's copy of what I added,
// as a program's is:
//
//	<name>/watcher.yaml   its description file, as I wrote it
//	<name>/<executable>   the executable it names, copied
//
// A watcher is polled (run every interval, given its cursor, printing what's
// new) or streams (one long-lived process printing events as they happen).
// Either way it prints JSON lines: events and its cursor (see ParseLine).
// Several hooks may listen to one watcher.
package watchers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
)

// DescriptionFile is a watcher's description file's name, in its folder.
const DescriptionFile = "watcher.yaml"

// Modes a watcher runs in.
const (
	// ModePoll is run every interval with its cursor, and prints what's new.
	ModePoll = "poll"
	// ModeStream is one long-lived process printing events as they happen,
	// started again (after a pause that grows) when it ends.
	ModeStream = "stream"
)

// Defaults and limits.
const (
	DefaultInterval = time.Minute
	MinInterval     = 5 * time.Second
	DefaultTimeout  = 30 * time.Second
)

// Watcher is one watcher, as the app keeps it.
type Watcher struct {
	Name        string
	Dir, Path   string
	Description string
	// Mode is ModePoll or ModeStream.
	Mode string
	// Interval is how often a poll watcher is run; Timeout how long one poll
	// may take.
	Interval, Timeout time.Duration
	// Program is what it runs: its copy of the executable, its arguments
	// and its secrets (it never writes).
	Program programs.Program
	// Events are the events it prints, as its file declares them, in its
	// order: a hook picks its type from them, and an event of another type
	// is dropped. None: it was written before watchers declared them, and
	// prints any type.
	Events []EventType
	Source string
}

// EventType is an event a watcher declares it prints: its type, and what
// it means in words ("End key pressed").
type EventType struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

// MaxLabel is the longest an event's label may be.
const MaxLabel = 80

// Declared reports whether it declares its events (one written before they
// were declared doesn't).
func (w Watcher) Declared() bool { return len(w.Events) > 0 }

// Declares reports whether it prints events of type t: one it declares, or
// any, when it declares none.
func (w Watcher) Declares(t string) bool {
	if !w.Declared() {
		return true
	}
	_, ok := w.Label(t)
	return ok
}

// Label is what an event of type t means, as it declares it.
func (w Watcher) Label(t string) (string, bool) {
	for _, e := range w.Events {
		if e.Type == t {
			return e.Label, true
		}
	}
	return "", false
}

// Types are the types it declares, in its order.
func (w Watcher) Types() []string {
	out := make([]string, len(w.Events))
	for i, e := range w.Events {
		out[i] = e.Type
	}
	return out
}

// eventList is the events key as written: event types, each with its label,
// kept in the file's order.
type eventList struct {
	set    bool
	events []EventType
}

func (l *eventList) UnmarshalYAML(n *yaml.Node) error {
	l.set = true
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: events: each event type with its label, like `key.end: End key pressed`", n.Line)
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch {
		case k.Kind != yaml.ScalarNode || !ValidType(k.Value):
			return fmt.Errorf("line %d: events: %q isn't an event type: letters, digits and . _ : / - (up to 100), like meeting.started", k.Line, k.Value)
		case seen[k.Value]:
			return fmt.Errorf("line %d: events: %s twice", k.Line, k.Value)
		case v.Kind != yaml.ScalarNode || v.Tag == "!!null" || strings.TrimSpace(v.Value) == "":
			return fmt.Errorf("line %d: events.%s: its label, what it means in words (like End key pressed)", v.Line, k.Value)
		case strings.ContainsAny(v.Value, "\r\n") || len([]rune(v.Value)) > MaxLabel:
			return fmt.Errorf("line %d: events.%s: a label is one line of at most %d characters", v.Line, k.Value, MaxLabel)
		}
		seen[k.Value] = true
		l.events = append(l.events, EventType{Type: k.Value, Label: strings.TrimSpace(v.Value)})
	}
	return nil
}

// file is the description file as written.
type file struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Executable  string            `yaml:"executable"`
	Args        []string          `yaml:"args"`
	Mode        string            `yaml:"mode"`
	Interval    string            `yaml:"interval"`
	Timeout     string            `yaml:"timeout"`
	Secrets     []programs.Secret `yaml:"secrets"`
	Permissions []string          `yaml:"permissions"`
	Events      eventList         `yaml:"events"`
}

// Fields are the description file's fields, for the guide and the docs.
var Fields = []programs.Field{
	{Name: "executable", Type: "path", Description: "Required. The executable, relative to this file's folder (or absolute). It's copied when the watcher is added."},
	{Name: "mode", Type: "poll or stream", Description: "Required. poll: run every interval, printing what's new since its cursor, then exit. stream: run once and keep printing events as they happen; started again when it ends."},
	{Name: "events", Type: "map", Description: "The events it prints: each type with its label, what it means in words (key.end: End key pressed). A hook picks its on.type from them, and its windows say the label; an event of another type is dropped. Left out, it may print any type."},
	{Name: "description", Type: "string", Description: "What it watches, in a line."},
	{Name: "name", Type: "string", Description: "Its name; when given, it must be the name it's added as."},
	{Name: "args", Type: "list", Description: "Arguments passed every time it starts."},
	{Name: "interval", Type: "duration", Description: "poll only: how often it's run (default 1m, at least 5s)."},
	{Name: "timeout", Type: "duration", Description: "poll only: how long one poll may take before it's stopped (default 30s)."},
	{Name: "secrets", Type: "list", Description: "Secrets it's given as environment variables: name, and optionally env and prompt."},
	programs.PermissionsField,
}

// ValidName reports whether name can name a watcher.
func ValidName(name string) bool { return programs.ValidName(name) }

// Parse validates a description file's text as watcher name's, without
// anything on disk.
func Parse(name, source string) (Watcher, error) {
	if err := programs.CheckName("a watcher", name, source); err != nil {
		return Watcher{}, err
	}
	var f file
	if err := yamlfile.DecodeStrict([]byte(source), &f); err != nil {
		return Watcher{}, fmt.Errorf("%s: %w", DescriptionFile, err)
	}
	if f.Name != "" && f.Name != name {
		return Watcher{}, fmt.Errorf("%s names it %q, not %q", DescriptionFile, f.Name, name)
	}
	if f.Events.set && len(f.Events.events) == 0 {
		return Watcher{}, fmt.Errorf("%s: events: list each event type it prints with its label, or leave events out", DescriptionFile)
	}
	w := Watcher{Name: name, Description: f.Description, Mode: f.Mode, Interval: DefaultInterval, Timeout: DefaultTimeout,
		Events: f.Events.events, Source: source}
	switch f.Mode {
	case ModePoll:
	case ModeStream:
		if f.Interval != "" || f.Timeout != "" {
			return Watcher{}, fmt.Errorf("%s: interval and timeout are for a poll watcher", DescriptionFile)
		}
	case "":
		return Watcher{}, fmt.Errorf("%s: mode is required: poll or stream", DescriptionFile)
	default:
		return Watcher{}, fmt.Errorf("%s: mode %q: poll or stream", DescriptionFile, f.Mode)
	}
	var err error
	if f.Interval != "" {
		if w.Interval, err = time.ParseDuration(f.Interval); err != nil || w.Interval < MinInterval {
			return Watcher{}, fmt.Errorf("%s: interval %q: a duration of at least %s", DescriptionFile, f.Interval, MinInterval)
		}
	}
	if f.Timeout != "" {
		if w.Timeout, err = time.ParseDuration(f.Timeout); err != nil || w.Timeout <= 0 {
			return Watcher{}, fmt.Errorf("%s: timeout %q: a duration, like 30s", DescriptionFile, f.Timeout)
		}
	}
	no := false
	if w.Program, err = programs.Build(name, DescriptionFile, programs.Spec{
		Description: f.Description, Executable: f.Executable, Args: f.Args, Writes: &no, Secrets: f.Secrets, Permissions: f.Permissions, Source: source,
	}); err != nil {
		return Watcher{}, err
	}
	return w, nil
}

// load reads the watcher in its folder dir.
func load(dir string) (Watcher, error) {
	name := filepath.Base(dir)
	raw, err := os.ReadFile(filepath.Join(dir, DescriptionFile))
	if err != nil {
		return Watcher{}, err
	}
	w, err := Parse(name, string(raw))
	if err != nil {
		return Watcher{}, err
	}
	if w.Program, err = programs.Locate(w.Program, dir, DescriptionFile); err != nil {
		return Watcher{}, err
	}
	w.Dir, w.Path = dir, w.Program.Path
	return w, nil
}

// FileError is a watcher that couldn't be loaded. Others still load.
type FileError struct {
	Name, Path string
	Err        error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }
func (e *FileError) Unwrap() error { return e.Err }

// Set is the content of the watchers folder.
type Set struct {
	Watchers []Watcher
	Errors   []*FileError
}

// Find is the watcher named name, if it loaded.
func (s *Set) Find(name string) (Watcher, bool) {
	for _, w := range s.Watchers {
		if w.Name == name {
			return w, true
		}
	}
	return Watcher{}, false
}

// Dir is the watchers folder inside the app's data folder.
func Dir(dataDir string) string { return filepath.Join(dataDir, "watchers") }

// Load reads the watchers folder: each watcher loads independently, and a
// missing folder has none.
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
		w, err := load(dir)
		if err != nil {
			set.Errors = append(set.Errors, &FileError{Name: e.Name(), Path: filepath.Join(dir, DescriptionFile), Err: err})
			continue
		}
		set.Watchers = append(set.Watchers, w)
	}
	sort.Slice(set.Watchers, func(i, j int) bool { return set.Watchers[i].Name < set.Watchers[j].Name })
	return set, nil
}

// Exists reports whether store has a watcher, loadable or not, named name.
func Exists(store, name string) bool {
	info, err := os.Stat(filepath.Join(store, name))
	return err == nil && info.IsDir()
}

// Add copies the watcher described by the file at descPath, and the
// executable it names, into store as name: a new one, or (replace) in place
// of the one there. Nothing changes if it fails.
func Add(store, name, descPath string, replace bool) (Watcher, error) {
	raw, err := os.ReadFile(descPath)
	if err != nil {
		return Watcher{}, err
	}
	w, err := Parse(name, string(raw))
	if err != nil {
		return Watcher{}, err
	}
	switch exists := Exists(store, name); {
	case exists && !replace:
		return Watcher{}, fmt.Errorf("watcher %q already exists (%s); update it instead", name, filepath.Join(store, name))
	case !exists && replace:
		return Watcher{}, fmt.Errorf("no watcher %q to update; add it instead", name)
	}
	if err := programs.Install(store, name, DescriptionFile, raw, programs.ExecutableBeside(descPath, w.Source), replace); err != nil {
		return Watcher{}, err
	}
	return load(filepath.Join(store, name))
}

// Delete removes a watcher's folder (a broken one too) and returns its
// description file's path.
func Delete(store, name string) (string, error) {
	if !ValidName(name) || !Exists(store, name) {
		return "", fmt.Errorf("no watcher %q", name)
	}
	dir := filepath.Join(store, name)
	return filepath.Join(dir, DescriptionFile), os.RemoveAll(dir)
}
