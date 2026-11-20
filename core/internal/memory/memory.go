// Package memory keeps what I want routines to remember: plain Markdown files
// in the app's data folder, one per scope (_global.md for every routine,
// <routine>.md for one), one memory per "- " line. I write them (the CLI, or
// by hand); routines only read them, through the {{> memories}} partial.
package memory

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// Global is the scope every routine gets.
const Global = ""

const globalFile = "_global.md"

// Memory is one thing to remember. Its ID is derived from its text (stable
// while the text is), and short enough to type.
type Memory struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Routine is the routine it's for; empty for every routine.
	Routine string `json:"routine,omitempty"`
}

// Store reads and writes the memory files in Dir.
type Store struct{ Dir string }

func (s Store) path(scope string) string {
	if scope == Global {
		return filepath.Join(s.Dir, globalFile)
	}
	return filepath.Join(s.Dir, scope+".md")
}

// ID is a memory's ID: the start of its text's hash.
func ID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:6]
}

// clean makes text one line.
func clean(text string) string { return strings.Join(strings.Fields(text), " ") }

// List returns a scope's memories, in file order.
func (s Store) List(scope string) ([]Memory, error) {
	f, err := os.Open(s.path(scope))
	if errors.Is(err, fs.ErrNotExist) {
		return []Memory{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := []Memory{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if text, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "- "); ok {
			if text = clean(text); text != "" {
				out = append(out, Memory{ID: ID(text), Text: text, Routine: scope})
			}
		}
	}
	return out, sc.Err()
}

// Scopes lists the scopes that have a file (the global one as "").
func (s Store) Scopes() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir() || !strings.HasSuffix(name, ".md"):
		case name == globalFile:
			out = append([]string{Global}, out...)
		default:
			out = append(out, strings.TrimSuffix(name, ".md"))
		}
	}
	return out, nil
}

// Size is how many characters a scope's memories take (what the cap counts).
func Size(ms []Memory) int {
	n := 0
	for _, m := range ms {
		n += len(m.Text)
	}
	return n
}

// Add appends a memory to a scope, refusing an exact duplicate.
func (s Store) Add(scope, text string) (Memory, error) {
	text = clean(text)
	if text == "" {
		return Memory{}, errors.New("a memory needs some text")
	}
	m := Memory{ID: ID(text), Text: text, Routine: scope}
	err := s.edit(scope, func(ms []Memory) ([]Memory, error) {
		for _, x := range ms {
			if x.ID == m.ID {
				return nil, fmt.Errorf("already remembered (%s)", x.ID)
			}
		}
		return append(ms, m), nil
	})
	return m, err
}

// Remove deletes a memory by ID, from whichever scope has it.
func (s Store) Remove(id string) (Memory, error) {
	return s.change(id, func(ms []Memory, i int) []Memory { return append(ms[:i], ms[i+1:]...) })
}

// Edit replaces a memory's text (it gets a new ID).
func (s Store) Edit(id, text string) (Memory, error) {
	text = clean(text)
	if text == "" {
		return Memory{}, errors.New("a memory needs some text; remove it instead")
	}
	m, err := s.change(id, func(ms []Memory, i int) []Memory {
		ms[i].Text, ms[i].ID = text, ID(text)
		return ms
	})
	m.Text, m.ID = text, ID(text)
	return m, err
}

// Replace sets a scope's memories (compacting).
func (s Store) Replace(scope string, texts []string) error {
	return s.edit(scope, func([]Memory) ([]Memory, error) {
		out := []Memory{}
		seen := map[string]bool{}
		for _, t := range texts {
			if t = clean(t); t != "" && !seen[ID(t)] {
				seen[ID(t)] = true
				out = append(out, Memory{ID: ID(t), Text: t, Routine: scope})
			}
		}
		return out, nil
	})
}

// Forget deletes a scope's file (its routine was deleted).
func (s Store) Forget(scope string) error {
	err := os.Remove(s.path(scope))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// change finds id in any scope and applies fn to that scope's memories.
func (s Store) change(id string, fn func(ms []Memory, i int) []Memory) (Memory, error) {
	scopes, err := s.Scopes()
	if err != nil {
		return Memory{}, err
	}
	for _, scope := range scopes {
		var found Memory
		err := s.edit(scope, func(ms []Memory) ([]Memory, error) {
			for i, m := range ms {
				if m.ID == id {
					found = m
					return fn(ms, i), nil
				}
			}
			return ms, nil
		})
		if err != nil {
			return Memory{}, err
		}
		if found.ID != "" {
			return found, nil
		}
	}
	return Memory{}, fmt.Errorf("no memory %q (see `"+product.Command+" memory list`)", id)
}

// edit rewrites a scope's file under a lock. Lines that aren't memories
// (headings, notes) are kept at the top.
func (s Store) edit(scope string, fn func([]Memory) ([]Memory, error)) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	path := s.path(scope)
	return atomicfile.Locked(path, func() error { return s.rewrite(scope, path, fn) })
}

func (s Store) rewrite(scope, path string, fn func([]Memory) ([]Memory, error)) error {
	ms, err := s.List(scope)
	if err != nil {
		return err
	}
	header, err := header(path, scope)
	if err != nil {
		return err
	}
	ms, err = fn(ms)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(header)
	for _, m := range ms {
		b.WriteString("- " + m.Text + "\n")
	}
	return atomicfile.Write(path, []byte(b.String()))
}

// header is a file's non-memory lines, or a default heading for a new file.
func header(path, scope string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		who := "every routine"
		if scope != Global {
			who = "the " + scope + " routine"
		}
		return "# Memories for " + who + "\n\nOne per line starting with \"- \". Edit here or with `" + product.Command + " memory`.\n\n", nil
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "- ") && line != "" {
			b.WriteString(line)
		}
	}
	return b.String(), nil
}

// Render is what a routine's prompt gets as {{> memories}}: the global
// memories and its own, or nothing when there are none.
func (s Store) Render(routine string) (string, error) {
	global, err := s.List(Global)
	if err != nil {
		return "", err
	}
	own, err := s.List(routine)
	if err != nil {
		return "", err
	}
	all := slices.Concat(global, own)
	if len(all) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("Things I've asked you to remember:\n")
	for _, m := range all {
		b.WriteString("- " + m.Text + "\n")
	}
	return b.String(), nil
}
