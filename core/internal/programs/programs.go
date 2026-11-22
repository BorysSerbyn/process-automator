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

	"go.yaml.in/yaml/v3"

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
	// Hook is set for a hook's program: its settings are hooks.<name>.<field>.
	Hook bool
}

// Key is the setting key of one of its fields: routines.<name>.<field>, or
// a hook's hooks.<name>.<field>.
func (p Program) Key(field string) string {
	if p.Hook {
		return "hooks." + p.Name + "." + field
	}
	return "routines." + p.Name + "." + field
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
	if p.Hook {
		return settings.HookSettings(p.Name, p.File, false)
	}
	return settings.RoutineSettingsOf(p.Name, p.File, settings.ProgramFields)
}

// Scheduled reports whether its file gives it a schedule.
func (p Program) Scheduled() bool { _, ok := p.File["schedule"]; return ok }

// ValidName reports whether name can name a program (its folder's name).
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Parse validates a description file's text as program name's, without
// anything on disk: the executable it names isn't looked at.
func Parse(name, source string) (Program, error) {
	if err := checkName("a program", name, source); err != nil {
		return Program{}, err
	}
	var f file
	if err := yamlfile.DecodeStrict([]byte(source), &f); err != nil {
		return Program{}, fmt.Errorf("%s: %w", DescriptionFile, err)
	}
	switch {
	case f.Name != "" && f.Name != name:
		return Program{}, fmt.Errorf("%s names it %q, not %q", DescriptionFile, f.Name, name)
	case f.Staleness != nil && f.Schedule == nil:
		return Program{}, fmt.Errorf("%s: staleness needs schedule", DescriptionFile)
	}
	settingsOf := map[string]any{}
	for k, v := range map[string]*string{"schedule": f.Schedule, "staleness": f.Staleness, "timeout": f.Timeout} {
		if v != nil {
			settingsOf[k] = *v
		}
	}
	return Build(name, DescriptionFile, Spec{
		Description: f.Description, Executable: f.Executable, Args: f.Args, Writes: f.Writes,
		Secrets: f.Secrets, File: settingsOf, Source: source,
	})
}

// checkName refuses a name that can't name what (a program, a hook, a
// watcher), and an empty description file.
func checkName(what, name, source string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q can't name %s: lowercase letters, digits and dashes", name, what)
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("the description file is empty")
	}
	return nil
}

// CheckName refuses a name that can't name what (a hook, a watcher), and an
// empty description file.
func CheckName(what, name, source string) error { return checkName(what, name, source) }

// Spec is what a description file says about the executable it runs: a
// program's, a hook's or a watcher's (each file has more keys of its own).
type Spec struct {
	Description string
	Executable  string
	Args        []string
	// Writes is required in a program's and a hook's file; a watcher never
	// writes (nil there means false).
	Writes  *bool
	Secrets []Secret
	// File are the settings the file sets, as written.
	File   map[string]any
	Source string
	// Hook makes it a hook's program (its settings are a hook's).
	Hook bool
}

// Build validates a spec as name's, from the description file named file
// (for messages), without anything on disk.
func Build(name, file string, s Spec) (Program, error) {
	switch {
	case strings.TrimSpace(s.Executable) == "":
		return Program{}, fmt.Errorf("%s: executable is required (the program's file, relative to %s)", file, file)
	case filepath.Base(s.Executable) == file:
		return Program{}, fmt.Errorf("%s: the executable can't be called %s", file, file)
	case s.Writes == nil:
		return Program{}, fmt.Errorf("%s: writes is required: true when it can change anything, else false", file)
	}
	p := Program{Name: name, Description: s.Description, Args: s.Args, Writes: *s.Writes, Source: s.Source, File: s.File, Hook: s.Hook}
	if p.File == nil {
		p.File = map[string]any{}
	}
	secrets, err := CheckSecrets(file, s.Secrets)
	if err != nil {
		return Program{}, err
	}
	p.Secrets = secrets
	// Validate the settings it sets with the same rules `settings set` uses.
	for _, def := range p.Settings() {
		if def.Default == nil {
			continue
		}
		if _, err := def.Normalize(def.Default); err != nil {
			return Program{}, fmt.Errorf("%s: %w", file, err)
		}
	}
	return p, nil
}

// CheckSecrets validates secrets as a description file (file) declares
// them, filling in each one's env.
func CheckSecrets(file string, in []Secret) ([]Secret, error) {
	var out []Secret
	seen := map[string]bool{}
	for _, s := range in {
		if !nameRE.MatchString(s.Name) {
			return nil, fmt.Errorf("%s: secret %q: names are lowercase letters, digits and dashes", file, s.Name)
		}
		if s.Env == "" {
			s.Env = strings.ToUpper(strings.ReplaceAll(s.Name, "-", "_"))
		}
		if !envRE.MatchString(s.Env) {
			return nil, fmt.Errorf("%s: secret %s: env %q isn't an environment variable's name", file, s.Name, s.Env)
		}
		if seen[s.Name] || seen["env "+s.Env] {
			return nil, fmt.Errorf("%s: secret %s is declared twice", file, s.Name)
		}
		seen[s.Name], seen["env "+s.Env] = true, true
		out = append(out, s)
	}
	return out, nil
}

// executableOf is the executable a description file names.
func executableOf(source string) string {
	var f struct {
		Executable string `yaml:"executable"`
	}
	_ = yaml.Unmarshal([]byte(source), &f)
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
	return Locate(p, dir, DescriptionFile)
}

// Locate is p as kept in its folder dir, with its description file named
// file there: its copy of the executable must be there.
func Locate(p Program, dir, file string) (Program, error) {
	p.Dir, p.Path = dir, filepath.Join(dir, file)
	p.Executable = filepath.Join(dir, filepath.Base(executableOf(p.Source)))
	if info, err := os.Stat(p.Executable); err != nil || !info.Mode().IsRegular() {
		return Program{}, fmt.Errorf("its executable %s is missing: add it again", p.Executable)
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
	switch exists := Exists(store, name); {
	case exists && !replace:
		return Program{}, fmt.Errorf("program %q already exists (%s); update it instead", name, filepath.Join(store, name))
	case !exists && replace:
		return Program{}, fmt.Errorf("no program %q to update; create it instead", name)
	}
	if err := Install(store, name, DescriptionFile, raw, ExecutableBeside(descPath, p.Source), replace); err != nil {
		return Program{}, err
	}
	return load(filepath.Join(store, name))
}

// ExecutableBeside is the executable a description file at descPath names:
// relative to its folder, or absolute.
func ExecutableBeside(descPath, source string) string {
	exe := executableOf(source)
	if !filepath.IsAbs(exe) {
		exe = filepath.Join(filepath.Dir(descPath), exe)
	}
	return exe
}

// Install puts a folder store/name in place holding the description file
// (file, with raw as its content) and a copy of the executable exe (none
// when exe is empty: a hook's prompt has none): a new folder, or (replace)
// in place of the one there. It's staged in a hidden folder and moved into
// place, so nothing changes if it fails.
func Install(store, name, file string, raw []byte, exe string, replace bool) error {
	if exe != "" {
		info, err := os.Stat(exe)
		switch {
		case err != nil:
			return fmt.Errorf("its executable: %w", err)
		case !info.Mode().IsRegular():
			return fmt.Errorf("its executable %s isn't a file", exe)
		case info.Mode().Perm()&0o111 == 0:
			return fmt.Errorf("its executable %s can't be run (chmod +x it)", exe)
		}
	}
	dir := filepath.Join(store, name)
	if err := os.MkdirAll(store, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(store, "."+name+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.WriteFile(filepath.Join(staging, file), raw, 0o600); err != nil {
		return err
	}
	if exe != "" {
		copied := filepath.Join(staging, filepath.Base(exe))
		if err := copyFile(exe, copied, 0o700); err != nil {
			return err
		}
		if err := unquarantine(copied); err != nil {
			return fmt.Errorf("its copy: %w", err)
		}
	}
	if replace {
		old := staging + ".old"
		if err := os.Rename(dir, old); err != nil {
			return err
		}
		if err := os.Rename(staging, dir); err != nil {
			_ = os.Rename(old, dir)
			return err
		}
		_ = os.RemoveAll(old)
		return nil
	}
	return os.Rename(staging, dir)
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
