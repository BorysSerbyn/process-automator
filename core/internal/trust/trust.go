// Package trust records which routines I trusted, and what they could reach
// when I did: their surface (servers, allowlisted tools, built-in tools,
// plugin sections, folder). A routine runs for real only while its surface is
// the one I trusted; any change that widens or alters what it can reach takes
// the trust away until I review it again. Prompt text, schedule, model and the
// like don't.
package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Surface is what a routine can reach.
type Surface struct {
	Servers      []string       `json:"servers"`
	Tools        []string       `json:"tools"`
	BuiltinTools []string       `json:"builtinTools,omitempty"`
	Folder       string         `json:"folder,omitempty"`
	Plugins      map[string]any `json:"plugins,omitempty"`
}

// Normalized sorts the lists, so equal surfaces compare equal.
func (s Surface) Normalized() Surface {
	sorted := func(l []string) []string {
		out := slices.Clone(l)
		sort.Strings(out)
		if out == nil {
			out = []string{}
		}
		return out
	}
	s.Servers, s.Tools, s.BuiltinTools = sorted(s.Servers), sorted(s.Tools), sorted(s.BuiltinTools)
	if len(s.BuiltinTools) == 0 {
		s.BuiltinTools = nil
	}
	if len(s.Plugins) == 0 {
		s.Plugins = nil
	}
	return s
}

// Equal reports whether two surfaces are the same.
func (s Surface) Equal(o Surface) bool {
	a, _ := json.Marshal(s.Normalized())
	b, _ := json.Marshal(o.Normalized())
	return string(a) == string(b)
}

// Changes describes how s differs from the trusted surface old.
func (s Surface) Changes(old Surface) []string {
	s, old = s.Normalized(), old.Normalized()
	var out []string
	list := func(what string, now, was []string) {
		for _, x := range now {
			if !slices.Contains(was, x) {
				out = append(out, fmt.Sprintf("adds %s %s", what, x))
			}
		}
		for _, x := range was {
			if !slices.Contains(now, x) {
				out = append(out, fmt.Sprintf("drops %s %s", what, x))
			}
		}
	}
	list("server", s.Servers, old.Servers)
	list("tool", s.Tools, old.Tools)
	list("built-in tool", s.BuiltinTools, old.BuiltinTools)
	if s.Folder != old.Folder {
		out = append(out, fmt.Sprintf("changes its folder from %q to %q", old.Folder, s.Folder))
	}
	names := map[string]bool{}
	for n := range s.Plugins {
		names[n] = true
	}
	for n := range old.Plugins {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		if !reflect.DeepEqual(s.Plugins[n], old.Plugins[n]) {
			out = append(out, fmt.Sprintf("changes its %s plugin settings", n))
		}
	}
	return out
}

// Record is one trusted routine.
type Record struct {
	Surface   Surface   `json:"surface"`
	TrustedAt time.Time `json:"trustedAt"`
}

// File is the trust file.
type File struct {
	SchemaVersion int               `json:"schemaVersion"`
	Routines      map[string]Record `json:"routines"`
}

// Store reads and writes the trust file.
type Store struct{ Path string }

// Load reads the file; ok is false when it doesn't exist yet.
func (st Store) Load() (f *File, ok bool, err error) {
	b, err := os.ReadFile(st.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{SchemaVersion: 1, Routines: map[string]Record{}}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	f = &File{}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, false, fmt.Errorf("%s: %w", st.Path, err)
	}
	if f.Routines == nil {
		f.Routines = map[string]Record{}
	}
	return f, true, nil
}

// Trust records routines as trusted with their surfaces.
func (st Store) Trust(surfaces map[string]Surface, now time.Time) error {
	return st.update(func(f *File) {
		for name, s := range surfaces {
			f.Routines[name] = Record{Surface: s.Normalized(), TrustedAt: now}
		}
	})
}

// Forget drops a routine's trust (it was deleted).
func (st Store) Forget(name string) error {
	return st.update(func(f *File) { delete(f.Routines, name) })
}

// Init trusts the given routines if there's no trust file yet: what was
// installed before trust existed keeps running.
func (st Store) Init(current map[string]Surface, now time.Time) error {
	return atomicfile.Locked(st.Path, func() error {
		f, ok, err := st.Load()
		if err != nil || ok {
			return err
		}
		for name, s := range current {
			f.Routines[name] = Record{Surface: s.Normalized(), TrustedAt: now}
		}
		return st.write(f)
	})
}

func (st Store) update(fn func(*File)) error {
	return atomicfile.Locked(st.Path, func() error {
		f, _, err := st.Load()
		if err != nil {
			return err
		}
		fn(f)
		return st.write(f)
	})
}

func (st Store) write(f *File) error {
	f.SchemaVersion = 1
	return atomicfile.WriteJSON(st.Path, f)
}
