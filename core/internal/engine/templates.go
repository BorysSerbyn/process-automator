package engine

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
)

// TemplateInfo is one row of `templates list --json`.
type TemplateInfo struct {
	templates.Template
	// RunsHere is false for templates for another OS (listed with --all).
	RunsHere bool `json:"runsHere"`
	// SupportsBackend is false for a template that doesn't work with the
	// selected backend (llm.backend): installing it is refused.
	SupportsBackend bool `json:"supportsBackend"`
	// Installed is true when a routine with the template's name exists.
	Installed bool `json:"installed"`
}

// Installable says the template can be installed now: for this OS and the
// selected backend, and not installed yet.
func (t TemplateInfo) Installable() bool { return t.RunsHere && t.SupportsBackend && !t.Installed }

// TemplatesDir is where the app's templates are: bundled next to the binary
// (product.TemplatesDir, relative to the executable), or the source checkout's
// in a development build.
func TemplatesDir() (string, error) {
	if dir := product.TemplatesDir; dir != "" {
		if filepath.IsAbs(dir) {
			return dir, nil
		}
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil { // the CLI is a symlink into the app
			return "", err
		}
		return filepath.Join(filepath.Dir(exe), dir), nil
	}
	if product.SourceDir != "" {
		return filepath.Join(product.SourceDir, "templates"), nil
	}
	return "", errors.New("this build doesn't know where its templates are")
}

// Templates is the templates for this OS (all: every OS), each saying
// whether a routine of its name is installed.
func (a *App) Templates(all bool) ([]TemplateInfo, error) {
	dir, err := TemplatesDir()
	if err != nil {
		return nil, err
	}
	list, err := templates.List(dir)
	if err != nil {
		return nil, err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return nil, err
	}
	backend, err := a.BackendDef()
	if err != nil {
		return nil, err
	}
	rows := []TemplateInfo{}
	for _, t := range list {
		if !all && !t.RunsHere() {
			continue
		}
		_, findErr := set.Find(t.Name)
		rows = append(rows, TemplateInfo{Template: t, RunsHere: t.RunsHere(), SupportsBackend: t.Routine.Supports(backend.Name),
			Installed: findErr == nil})
	}
	return rows, nil
}
