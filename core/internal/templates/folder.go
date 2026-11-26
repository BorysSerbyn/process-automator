package templates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
)

// A template folder is a program, a hook or a watcher: its description file
// (program.yaml, hook.yaml or hook.prompt, watcher.yaml), its executable or
// the source to build it from, and ManifestFile, which says how it's
// installed. Installing one stages its files (parameters filled in, the
// executable built when it declares a build) and adds them as the commands
// that add one do.

// ManifestFile marks a folder as a template, and says how it's installed.
const ManifestFile = "template.yaml"

// OutputPlaceholder is, in a build's command, the path the build writes the
// executable to.
const OutputPlaceholder = "{{output}}"

// BuildTimeout is how long a template's build may take.
var BuildTimeout = 10 * time.Minute

// Kinds of template.
const (
	KindRoutine = "routine"
	KindProgram = "program"
	KindHook    = "hook"
	KindWatcher = "watcher"
)

// Build is how a template's executable is built from its source when it's
// installed.
type Build struct {
	// Command is run with its first word looked up on my PATH; one argument
	// holds OutputPlaceholder, where the executable must be written.
	Command []string `yaml:"command" json:"command"`
	// Dir is the folder it runs in, relative to the template's (its source,
	// within the templates folder, e.g. one several templates share): it's
	// copied to a temporary folder first. Default: the template's own.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// manifest is ManifestFile.
type manifest struct {
	Version     string       `yaml:"version"`
	Changes     string       `yaml:"changes"`
	Description string       `yaml:"description"`
	Platforms   []string     `yaml:"platforms"`
	Params      []paramInput `yaml:"params"`
	Build       *Build       `yaml:"build"`
}

// paramInput is a parameter as ManifestFile declares it.
type paramInput struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Default     any      `yaml:"default"`
	Description string   `yaml:"description"`
	Enum        []string `yaml:"enum"`
}

// descriptionFiles are the files a template folder's kind comes from.
var descriptionFiles = map[string]string{
	programs.DescriptionFile: KindProgram,
	hooks.DescriptionFile:    KindHook,
	hooks.PromptFile:         KindHook,
	watchers.DescriptionFile: KindWatcher,
}

// isTemplateFolder reports whether dir holds ManifestFile.
func isTemplateFolder(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ManifestFile))
	return err == nil
}

// loadFolder reads the template folder dir, under the templates folder
// root, and checks its description file parses with its parameters filled
// in (their defaults, or a sample of their type).
func loadFolder(root, dir string) (Template, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Template{}, err
	}
	var m manifest
	if err := yamlfile.DecodeStrict(raw, &m); err != nil {
		return Template{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	name := filepath.Base(dir)
	if !programs.ValidName(name) {
		return Template{}, fmt.Errorf("%q can't name a template: lowercase letters, digits and dashes", name)
	}
	rel, _ := filepath.Rel(root, dir)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	tags := parts[:len(parts)-1]
	t := Template{
		ID: strings.Join(parts, "/"), Name: name, Tags: tags, Project: projectOf(tags),
		Description: m.Description, Platforms: m.Platforms, Build: m.Build, Dir: dir, Root: root,
		Version: strings.TrimSpace(m.Version), Changes: strings.TrimSpace(m.Changes),
	}
	for _, p := range m.Platforms {
		if !slices.Contains(routines.Platforms, p) {
			return Template{}, fmt.Errorf("%s: platforms: %q isn't one of %s", ManifestFile, p, strings.Join(routines.Platforms, ", "))
		}
	}
	params := make([]routines.Param, len(m.Params))
	for i, p := range m.Params {
		params[i] = routines.Param{Name: p.Name, Type: p.Type, Default: p.Default, Description: p.Description, Enum: p.Enum}
	}
	if t.Params, err = routines.CheckParams(params); err != nil {
		return Template{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	for file, kind := range descriptionFiles {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			continue
		}
		if t.DescFile != "" {
			return Template{}, fmt.Errorf("it has both %s and %s: a template is one program, hook or watcher", t.DescFile, file)
		}
		t.DescFile, t.Kind = file, kind
	}
	if t.DescFile == "" {
		return Template{}, fmt.Errorf("it has no %s, %s, %s or %s beside its %s", programs.DescriptionFile, hooks.DescriptionFile,
			hooks.PromptFile, watchers.DescriptionFile, ManifestFile)
	}
	src, err := os.ReadFile(filepath.Join(dir, t.DescFile))
	if err != nil {
		return Template{}, err
	}
	t.Source = string(src)
	if err := t.parse(t.Fill(samples(t.Params))); err != nil {
		return Template{}, fmt.Errorf("%s: %w", t.DescFile, err)
	}
	if t.Hash, err = t.folderHash(); err != nil {
		return Template{}, err
	}
	return t, nil
}

// parse parses the description file's text as its kind's, and notes what
// it says that installing needs.
func (t *Template) parse(text string) error {
	path := filepath.Join(t.Dir, t.DescFile)
	switch t.Kind {
	case KindProgram:
		p, err := programs.Parse(t.Name, text)
		if err != nil {
			return err
		}
		t.describe(p.Description)
	case KindHook:
		h, err := hooks.Parse(t.Name, path, text)
		if err != nil {
			return err
		}
		if err := hooks.RequireOutcome(h); err != nil {
			return err
		}
		t.describe(h.Description)
		t.Watcher, t.On = h.On.Watcher, h.On.Type
	case KindWatcher:
		w, err := watchers.Parse(t.Name, text)
		if err != nil {
			return err
		}
		t.describe(w.Description)
		for _, e := range w.Events {
			t.Events = append(t.Events, e.Type)
		}
	}
	return nil
}

func (t *Template) describe(d string) {
	if t.Description == "" {
		t.Description = d
	}
}

// samples are values for params: their defaults, else one of their type.
func samples(params []routines.Param) map[string]string {
	out := map[string]string{}
	for _, p := range params {
		v := p.DefaultText
		if p.Required {
			switch {
			case len(p.Enum) > 0:
				v = p.Enum[0]
			case p.Type == "int", p.Type == "number", p.Type == "percent":
				v = "1"
			case p.Type == "cron":
				v = "0 9 * * *"
			case p.Type == "bool":
				v = "true"
			case p.Type == "duration":
				v = "1h"
			default:
				v = "sample"
			}
		}
		out[p.Name] = v
	}
	return out
}

// Fill is the description file's text with its parameters' values
// ({{name}}) filled in; values missing one use its default.
func (t Template) Fill(values map[string]string) string {
	text := t.Source
	for _, p := range t.Params {
		v, ok := values[p.Name]
		if !ok {
			v = p.DefaultText
		}
		text = strings.ReplaceAll(text, "{{"+p.Name+"}}", v)
	}
	return text
}

// Executable is the executable's path the description file names,
// relative to the template's folder ("" for an LLM hook, which has none).
func (t Template) Executable() string {
	if t.Kind == KindRoutine || t.DescFile == hooks.PromptFile {
		return ""
	}
	exe := programs.ExecutableBeside(filepath.Join(t.Dir, t.DescFile), t.Source)
	rel, err := filepath.Rel(t.Dir, exe)
	if err != nil {
		return exe
	}
	return rel
}

// Stage writes what installing a folder template adds into a new folder
// under tmp: its description file with values filled in, and its
// executable (built, when it declares a build; the build's output goes to
// log). It returns the description file's path there.
func (t Template) Stage(ctx context.Context, values map[string]string, tmp string, log io.Writer) (string, error) {
	stage := filepath.Join(tmp, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return "", err
	}
	desc := filepath.Join(stage, t.DescFile)
	if err := os.WriteFile(desc, []byte(t.Fill(values)), 0o600); err != nil {
		return "", err
	}
	rel := t.Executable()
	if rel == "" {
		return desc, nil
	}
	exe := filepath.Join(stage, rel)
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		return "", err
	}
	if t.Build == nil {
		return desc, copyFile(filepath.Join(t.Dir, rel), exe)
	}
	if err := t.build(ctx, filepath.Join(tmp, "src"), exe, log); err != nil {
		return "", err
	}
	return desc, nil
}

// build runs the template's build in a copy of its folder at src, writing
// the executable to out.
func (t Template) build(ctx context.Context, src, out string, log io.Writer) error {
	b := t.Build
	if _, err := exec.LookPath(b.Command[0]); err != nil {
		return fmt.Errorf("installing %s builds it with %s, which isn't installed (not found on PATH): install it, then try again",
			t.ID, b.Command[0])
	}
	if err := copyTree(filepath.Join(t.Dir, b.Dir), src); err != nil {
		return err
	}
	args := make([]string, len(b.Command)-1)
	for i, a := range b.Command[1:] {
		args[i] = strings.ReplaceAll(a, OutputPlaceholder, out)
	}
	ctx, cancel := context.WithTimeout(ctx, BuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.Command[0], args...)
	cmd.Dir = src
	var said tail
	cmd.Stdout, cmd.Stderr = io.MultiWriter(log, &said), io.MultiWriter(log, &said)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s (%s) failed: %w\n%s", t.ID, strings.Join(b.Command, " "), err, said.String())
	}
	if info, err := os.Stat(out); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("building %s (%s) wrote no executable at %s", t.ID, strings.Join(b.Command, " "), OutputPlaceholder)
	}
	return nil
}

// tail keeps the last 2 KiB written to it: what a failed build said.
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string { return strings.TrimSpace(string(t.b)) }

// placeholderRE finds {{name}} in a description file.
var placeholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// checkFolder is what a template folder must pass beyond loading: its
// executable there (or a well-formed build), its placeholders declared, and
// a hook's watcher a template of the same templates.
func checkFolder(t Template, all []Template) error {
	var errs []error
	if t.DescFile != hooks.PromptFile {
		for _, m := range placeholderRE.FindAllStringSubmatch(t.Source, -1) {
			if !slices.ContainsFunc(t.Params, func(p routines.Param) bool { return p.Name == m[1] }) {
				errs = append(errs, fmt.Errorf("%s uses {{%s}}, which isn't one of its params", t.DescFile, m[1]))
			}
		}
	}
	if rel := t.Executable(); rel != "" {
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			errs = append(errs, fmt.Errorf("its executable %s isn't in the template's folder", rel))
		}
		if t.Build == nil {
			info, err := os.Stat(filepath.Join(t.Dir, rel))
			if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
				errs = append(errs, fmt.Errorf("its executable %s isn't there, or can't be run (or declare a build)", rel))
			}
		}
	} else if t.Build != nil {
		errs = append(errs, errors.New("an LLM hook has no executable to build"))
	}
	if b := t.Build; b != nil {
		switch {
		case len(b.Command) == 0 || strings.TrimSpace(b.Command[0]) == "":
			errs = append(errs, errors.New("build.command is empty"))
		case !slices.ContainsFunc(b.Command[1:], func(a string) bool { return strings.Contains(a, OutputPlaceholder) }):
			errs = append(errs, fmt.Errorf("build.command doesn't say where the executable goes (%s)", OutputPlaceholder))
		}
		dir := filepath.Join(t.Dir, b.Dir)
		if rel, err := filepath.Rel(t.Root, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			errs = append(errs, fmt.Errorf("build.dir %s is outside the templates folder", b.Dir))
		} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			errs = append(errs, fmt.Errorf("build.dir %s isn't a folder", b.Dir))
		}
	}
	if t.Kind == KindHook {
		if _, err := WatcherOf(all, t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WatcherOf is the watcher template a hook template listens to: the one
// watcher template named as its watcher, declaring its event type.
func WatcherOf(all []Template, hook Template) (Template, error) {
	var found []Template
	for _, w := range all {
		if w.Kind == KindWatcher && w.Name == hook.Watcher {
			found = append(found, w)
		}
	}
	switch {
	case len(found) == 0:
		return Template{}, fmt.Errorf("it listens to the watcher %s, and no template is that watcher", hook.Watcher)
	case len(found) > 1:
		return Template{}, fmt.Errorf("it listens to the watcher %s, and %d templates are that watcher", hook.Watcher, len(found))
	}
	w := found[0]
	if hook.On != "" && len(w.Events) > 0 && !slices.Contains(w.Events, hook.On) {
		return Template{}, fmt.Errorf("it listens for %s, which the watcher %s (%s) doesn't declare (%s)", hook.On, w.Name, w.ID, strings.Join(w.Events, ", "))
	}
	return w, nil
}

// copyFile copies src to dst, keeping its mode.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, info.Mode().Perm())
}

// copyTree copies the folder src to dst (made), files and folders only.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o700)
		case d.Type().IsRegular():
			return copyFile(path, to)
		}
		return nil
	})
}
