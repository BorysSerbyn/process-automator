// Package trust records which routines I trusted, and with what: an LLM
// routine's surface (servers, allowlisted tools, built-in tools, plugin
// sections, folder), or a program's executable and description file. A
// routine runs for real only while that is what I trusted; any change that
// widens or alters what it can reach or run takes the trust away until I
// review it again. An LLM routine's prompt text, its schedule, model and the
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

// Program is what a program was trusted with: its executable and its
// description file (its arguments, writes and secrets with it), by checksum,
// and what the file declares, to say what changed.
type Program struct {
	// Executable and Description are the SHA-256 of its executable and of
	// its description file.
	Executable  string `json:"executable"`
	Description string `json:"description"`
	Writes      bool   `json:"writes"`
	// Secrets are the secrets it's given.
	Secrets []string `json:"secrets,omitempty"`
}

// Approval is what a routine is trusted with: an LLM routine's surface, or a
// program's section.
type Approval struct {
	Surface *Surface `json:"surface,omitempty"`
	Program *Program `json:"program,omitempty"`
}

// Normalized sorts what's listed, so equal approvals compare equal.
func (a Approval) Normalized() Approval {
	if a.Surface != nil {
		s := a.Surface.Normalized()
		a.Surface = &s
	}
	if a.Program != nil {
		p := *a.Program
		p.Secrets = slices.Clone(p.Secrets)
		sort.Strings(p.Secrets)
		if len(p.Secrets) == 0 {
			p.Secrets = nil
		}
		a.Program = &p
	}
	return a
}

// Equal reports whether two approvals are the same.
func (a Approval) Equal(o Approval) bool {
	x, _ := json.Marshal(a.Normalized())
	y, _ := json.Marshal(o.Normalized())
	return string(x) == string(y)
}

// Changes describes how a differs from the trusted approval old.
func (a Approval) Changes(old Approval) []string {
	a, old = a.Normalized(), old.Normalized()
	switch {
	case a.Program != nil && old.Program == nil:
		return []string{"is a program now, not an LLM routine"}
	case a.Program == nil && old.Program != nil:
		return []string{"is an LLM routine now, not a program"}
	case a.Program != nil:
		return a.Program.changes(*old.Program)
	case a.Surface != nil && old.Surface != nil:
		return a.Surface.Changes(*old.Surface)
	case a.Surface != nil:
		return a.Surface.Changes(Surface{})
	}
	return nil
}

func (p Program) changes(old Program) []string {
	var out []string
	if p.Executable != old.Executable {
		out = append(out, "has another executable")
	}
	if p.Writes != old.Writes {
		if p.Writes {
			out = append(out, "now writes")
		} else {
			out = append(out, "no longer writes")
		}
	}
	for _, s := range p.Secrets {
		if !slices.Contains(old.Secrets, s) {
			out = append(out, "adds secret "+s)
		}
	}
	for _, s := range old.Secrets {
		if !slices.Contains(p.Secrets, s) {
			out = append(out, "drops secret "+s)
		}
	}
	if len(out) == 0 && p.Description != old.Description {
		out = append(out, "changes its description file")
	}
	return out
}

// Record is one trusted routine: what it was trusted with, and when.
type Record struct {
	Approval
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

// Trust records routines as trusted with what they're approved for.
func (st Store) Trust(approvals map[string]Approval, now time.Time) error {
	return st.update(func(f *File) {
		for name, a := range approvals {
			f.Routines[name] = Record{Approval: a.Normalized(), TrustedAt: now}
		}
	})
}

// Forget drops a routine's trust (it was deleted).
func (st Store) Forget(name string) error {
	return st.update(func(f *File) { delete(f.Routines, name) })
}

// Init trusts the given routines if there's no trust file yet: what was
// installed before trust existed keeps running.
func (st Store) Init(current map[string]Approval, now time.Time) error {
	return atomicfile.Locked(st.Path, func() error {
		f, ok, err := st.Load()
		if err != nil || ok {
			return err
		}
		for name, a := range current {
			f.Routines[name] = Record{Approval: a.Normalized(), TrustedAt: now}
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
