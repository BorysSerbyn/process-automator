package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A template's hash is its content's: a routine template's file, a folder
// template's files and its build's folder (whose source may be shared with
// other templates). Installing records it, so a template whose hash differs
// from the one installed is offered as an update; its version only says
// where it's at.

// TextHash is the hash of a file's text, as hashes are written
// ("sha256:<hex>").
func TextHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileHash is the hash of the file at path.
func FileHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return TextHash(string(b)), nil
}

// folderHash hashes the template's folder and its build's, each file by its
// path relative to the templates folder, its mode's executable bit and its
// content, in path order.
func (t Template) folderHash() (string, error) {
	dirs := []string{t.Dir}
	if t.Build != nil && t.Build.Dir != "" {
		if d := filepath.Join(t.Dir, t.Build.Dir); d != t.Dir {
			dirs = append(dirs, d)
		}
	}
	type entry struct{ rel, line string }
	var entries []entry
	seen := map[string]bool{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			rel, err := filepath.Rel(t.Root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if seen[rel] {
				return nil
			}
			seen[rel] = true
			info, err := d.Info()
			if err != nil {
				return err
			}
			h, err := FileHash(path)
			if err != nil {
				return err
			}
			exec := "-"
			if info.Mode()&0o111 != 0 {
				exec = "x"
			}
			entries = append(entries, entry{rel, rel + " " + exec + " " + h})
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.line)
		b.WriteByte('\n')
	}
	return TextHash(b.String()), nil
}
