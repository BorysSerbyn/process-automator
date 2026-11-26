// Package installs remembers what was installed from a template: for each
// routine, program, hook and watcher (they share one name space), which
// template and version it came from, the template's hash then and the hash
// of what was written, and the parameter values filled into it. Comparing
// them with the template shipped now says whether it changed since, and
// with the files there now whether I edited my copy.
package installs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// SchemaVersion is the file's format.
const SchemaVersion = 1

// Record is one install.
type Record struct {
	// Template is the template's ID, Kind what it installed (routine,
	// program, hook, watcher), Version its version then ("" for none).
	Template string `json:"template"`
	Kind     string `json:"kind"`
	Version  string `json:"version,omitempty"`
	// Hash is the template's hash when it was installed; "" for an install
	// matched to its template afterwards whose content differed (so any
	// template is newer than it).
	Hash string `json:"hash"`
	// Installed is the hash of what was written (Hashes); "" when unknown.
	Installed string `json:"installed,omitempty"`
	// Values are the parameter values filled into a folder template's
	// description file (a routine's are its settings).
	Values map[string]string `json:"values,omitempty"`
	// Skipped is the template hash I chose not to take: it isn't offered
	// again, a later one is.
	Skipped string `json:"skipped,omitempty"`
	// Matched is that it was installed before installs were recorded, and
	// matched to its template by name and kind.
	Matched bool `json:"matched,omitempty"`
}

// File is the store's content.
type File struct {
	SchemaVersion int `json:"schemaVersion"`
	// Installs are by the name installed as.
	Installs map[string]Record `json:"installs"`
}

// Store is the file at Path.
type Store struct{ Path string }

// Load reads the file; a missing one is empty.
func (s Store) Load() (*File, error) { return load(s.Path) }

func load(path string) (*File, error) {
	f := &File{SchemaVersion: SchemaVersion, Installs: map[string]Record{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("%s is from a newer version of the app (schema %d)", path, f.SchemaVersion)
	}
	if f.Installs == nil {
		f.Installs = map[string]Record{}
	}
	f.SchemaVersion = SchemaVersion
	return f, nil
}

// Change changes the file under its lock.
func (s Store) Change(fn func(f *File) error) error {
	return atomicfile.UpdateJSON(s.Path, load, fn)
}

// Set records name's install.
func (s Store) Set(name string, r Record) error {
	return s.Change(func(f *File) error {
		f.Installs[name] = r
		return nil
	})
}

// Forget removes name's record, when there's one.
func (s Store) Forget(name string) error {
	return s.Change(func(f *File) error {
		delete(f.Installs, name)
		return nil
	})
}
