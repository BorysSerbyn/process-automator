// Package programs keeps programs on the user's device: routines that run an
// executable instead of a model. Each is a folder of its own in the programs
// folder, the app's copy of what I added:
//
//	<name>/program.yaml   its description file, as I wrote it
//	<name>/<executable>   the executable it names, copied
//
// The app runs that copy, never the file it was added from, so a file
// changed or swapped where I built it can't run in its place.
package programs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
)

// DescriptionFile is a program's description file's name, in its folder.
const DescriptionFile = "program.yaml"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// envRE: an environment variable's name.
var envRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Program is one program, as the app keeps it.
type Program struct {
	Name string
	// Dir is its folder in the programs folder; Path its description file there.
	Dir, Path string
	// Executable is the copy that runs, in Dir.
	Executable  string
	Description string
	// Args are passed to it on every run (before --dry-run, on a dry run).
	Args []string
	// Writes is whether it can change anything: a dry run passes it
	// --dry-run, and a run cut off by sleep isn't run again.
	Writes bool
	// Secrets are the secrets it's given, as environment variables.
	Secrets []Secret
	// File holds the per-routine settings its file sets (schedule,
	// staleness, timeout), as written. Effective values come from settings.
	File map[string]any
	// Source is the description file as written.
	Source string
}

// Secret is a secret a program declares: kept in the secret store under its
// program's name, passed to it as the environment variable Env.
type Secret struct {
	Name string `yaml:"name" json:"name"`
	// Env is the environment variable it's passed as (default: its name in
	// capitals, dashes as underscores).
	Env string `yaml:"env,omitempty" json:"env"`
	// Prompt is what signing in asks for (default: its name).
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
}

// file is the description file as written.
type file struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Executable  string   `yaml:"executable"`
	Args        []string `yaml:"args"`
	Writes      *bool    `yaml:"writes"`
	Schedule    *string  `yaml:"schedule"`
	Staleness   *string  `yaml:"staleness"`
	Timeout     *string  `yaml:"timeout"`
	Secrets     []Secret `yaml:"secrets"`
}

// Field is one of the description file's fields.
type Field struct {
	Name, Type, Description string
}

// Fields are the description file's fields, for the guide and the docs.
var Fields = []Field{
	{"executable", "path", "Required. The executable, relative to this file's folder (or absolute). It's copied when the program is added."},
	{"writes", "bool", "Required. Whether it can change anything. A dry run passes --dry-run to one that writes, and runs one that doesn't as it is."},
	{"description", "string", "What it does, in a line."},
	{"name", "string", "Its name; when given, it must be the name it's added as."},
	{"args", "list", "Arguments passed on every run."},
	{"schedule", "cron", "When it runs (local time); left out, only by hand."},
	{"staleness", "duration", "How late a missed run may still run (needs schedule)."},
	{"timeout", "duration", "How long a run may take before it's stopped."},
	{"secrets", "list", "Secrets it's given as environment variables: name, and optionally env and prompt."},
}

// Settings are the program's per-routine settings (routines.<name>.<field>),
// with defaults from its file.
func (p Program) Settings() []settings.Setting {
	return settings.RoutineSettingsOf(p.Name, p.File, settings.ProgramFields)
}

// Scheduled reports whether its file gives it a schedule.
func (p Program) Scheduled() bool { _, ok := p.File["schedule"]; return ok }

// ValidName reports whether name can name a program (its folder's name).
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Parse validates a description file's text as program name's, without
// anything on disk: the executable it names isn't looked at.
func Parse(name, source string) (Program, error) {
	if !nameRE.MatchString(name) {
		return Program{}, fmt.Errorf("%q can't name a program: lowercase letters, digits and dashes", name)
	}
	if strings.TrimSpace(source) == "" {
		return Program{}, errors.New("the description file is empty")
	}
	var f file
	if err := yamlfile.DecodeStrict([]byte(source), &f); err != nil {
		return Program{}, fmt.Errorf("%s: %w", DescriptionFile, err)
	}
	switch {
	case f.Name != "" && f.Name != name:
		return Program{}, fmt.Errorf("%s names it %q, not %q", DescriptionFile, f.Name, name)
	case strings.TrimSpace(f.Executable) == "":
		return Program{}, fmt.Errorf("%s: executable is required (the program's file, relative to %s)", DescriptionFile, DescriptionFile)
	case filepath.Base(f.Executable) == DescriptionFile:
		return Program{}, fmt.Errorf("%s: the executable can't be called %s", DescriptionFile, DescriptionFile)
	case f.Writes == nil:
		return Program{}, fmt.Errorf("%s: writes is required: true when it can change anything, else false", DescriptionFile)
	case f.Staleness != nil && f.Schedule == nil:
		return Program{}, fmt.Errorf("%s: staleness needs schedule", DescriptionFile)
	}
	p := Program{Name: name, Description: f.Description, Args: f.Args, Writes: *f.Writes, Source: source, File: map[string]any{}}
	for k, v := range map[string]*string{"schedule": f.Schedule, "staleness": f.Staleness, "timeout": f.Timeout} {
		if v != nil {
			p.File[k] = *v
		}
	}
	seen := map[string]bool{}
	for _, s := range f.Secrets {
		if !nameRE.MatchString(s.Name) {
			return Program{}, fmt.Errorf("%s: secret %q: names are lowercase letters, digits and dashes", DescriptionFile, s.Name)
		}
		if s.Env == "" {
			s.Env = strings.ToUpper(strings.ReplaceAll(s.Name, "-", "_"))
		}
		if !envRE.MatchString(s.Env) {
			return Program{}, fmt.Errorf("%s: secret %s: env %q isn't an environment variable's name", DescriptionFile, s.Name, s.Env)
		}
		if seen[s.Name] || seen["env "+s.Env] {
			return Program{}, fmt.Errorf("%s: secret %s is declared twice", DescriptionFile, s.Name)
		}
		seen[s.Name], seen["env "+s.Env] = true, true
		p.Secrets = append(p.Secrets, s)
	}
	// Validate the settings it sets with the same rules `settings set` uses.
	for _, def := range p.Settings() {
		if def.Default == nil {
			continue
		}
		if _, err := def.Normalize(def.Default); err != nil {
			return Program{}, fmt.Errorf("%s: %w", DescriptionFile, err)
		}
	}
	return p, nil
}

// executableOf is the executable a description file names.
func executableOf(source string) string {
	var f file
	_ = yamlfile.DecodeStrict([]byte(source), &f)
	return f.Executable
}

// load reads the program in its folder dir.
func load(dir string) (Program, error) {
	name := filepath.Base(dir)
	path := filepath.Join(dir, DescriptionFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Program{}, err
	}
	p, err := Parse(name, string(raw))
	if err != nil {
		return Program{}, err
	}
	p.Dir, p.Path = dir, path
	p.Executable = filepath.Join(dir, filepath.Base(executableOf(p.Source)))
	if info, err := os.Stat(p.Executable); err != nil || !info.Mode().IsRegular() {
		return Program{}, fmt.Errorf("its executable %s is missing: add the program again", p.Executable)
	}
	return p, nil
}

// FileError is a program that couldn't be loaded. Others still load.
type FileError struct {
	Name, Path string
	Err        error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }
func (e *FileError) Unwrap() error { return e.Err }

// Set is the content of the programs folder.
type Set struct {
	Programs []Program
	// Errors lists programs that couldn't be loaded; they're skipped.
	Errors []*FileError
}

// Dir is the programs folder inside the app's data folder.
func Dir(dataDir string) string { return filepath.Join(dataDir, "programs") }

// Load reads the programs folder: each program loads independently, and a
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
			continue // staging folders are hidden
		}
		dir := filepath.Join(store, e.Name())
		p, err := load(dir)
		if err != nil {
			set.Errors = append(set.Errors, &FileError{Name: e.Name(), Path: filepath.Join(dir, DescriptionFile), Err: err})
			continue
		}
		set.Programs = append(set.Programs, p)
	}
	sort.Slice(set.Programs, func(i, j int) bool { return set.Programs[i].Name < set.Programs[j].Name })
	return set, nil
}

// Exists reports whether store has a program, loadable or not, named name.
func Exists(store, name string) bool {
	info, err := os.Stat(filepath.Join(store, name))
	return err == nil && info.IsDir()
}

// Add copies the program described by the file at descPath, and the
// executable it names, into store as name: a new program, or (replace) in
// place of the one there. Nothing changes if it fails.
func Add(store, name, descPath string, replace bool) (Program, error) {
	raw, err := os.ReadFile(descPath)
	if err != nil {
		return Program{}, err
	}
	p, err := Parse(name, string(raw))
	if err != nil {
		return Program{}, err
	}
	exe := executableOf(p.Source)
	if !filepath.IsAbs(exe) {
		exe = filepath.Join(filepath.Dir(descPath), exe)
	}
	info, err := os.Stat(exe)
	switch {
	case err != nil:
		return Program{}, fmt.Errorf("its executable: %w", err)
	case !info.Mode().IsRegular():
		return Program{}, fmt.Errorf("its executable %s isn't a file", exe)
	case info.Mode().Perm()&0o111 == 0:
		return Program{}, fmt.Errorf("its executable %s can't be run (chmod +x it)", exe)
	}
	dir := filepath.Join(store, name)
	switch exists := Exists(store, name); {
	case exists && !replace:
		return Program{}, fmt.Errorf("program %q already exists (%s); update it instead", name, dir)
	case !exists && replace:
		return Program{}, fmt.Errorf("no program %q to update; create it instead", name)
	}
	if err := os.MkdirAll(store, 0o700); err != nil {
		return Program{}, err
	}
	staging, err := os.MkdirTemp(store, "."+name+"-")
	if err != nil {
		return Program{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.WriteFile(filepath.Join(staging, DescriptionFile), raw, 0o600); err != nil {
		return Program{}, err
	}
	copied := filepath.Join(staging, filepath.Base(exe))
	if err := copyFile(exe, copied, 0o700); err != nil {
		return Program{}, err
	}
	if err := unquarantine(copied); err != nil {
		return Program{}, fmt.Errorf("its copy: %w", err)
	}
	if replace {
		old := staging + ".old"
		if err := os.Rename(dir, old); err != nil {
			return Program{}, err
		}
		if err := os.Rename(staging, dir); err != nil {
			_ = os.Rename(old, dir)
			return Program{}, err
		}
		_ = os.RemoveAll(old)
	} else if err := os.Rename(staging, dir); err != nil {
		return Program{}, err
	}
	return load(dir)
}

// Delete removes a program's folder (a broken one too) and returns its
// description file's path.
func Delete(store, name string) (string, error) {
	if !nameRE.MatchString(name) || !Exists(store, name) {
		return "", fmt.Errorf("no program %q", name)
	}
	dir := filepath.Join(store, name)
	return filepath.Join(dir, DescriptionFile), os.RemoveAll(dir)
}

// Checksum is a file's SHA-256, in hex.
func Checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ChecksumText is a text's SHA-256, in hex.
func ChecksumText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func copyFile(from, to string, perm os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
