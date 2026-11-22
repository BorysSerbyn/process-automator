package engine

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// RunConfig is the settings every routine has, whatever its kind: when it
// runs, for how long, and how the shared machinery treats its runs.
type RunConfig struct {
	Enabled   bool
	Schedule  string        // empty: runs only by hand
	Staleness time.Duration // scheduled routines only
	Timeout   time.Duration
	// WorkFolder is where its runs work (an LLM routine's: see
	// RoutineConfig.Folder; a program's: its data folder); empty: an empty
	// temporary folder, deleted after.
	WorkFolder string
	// Queued is whether its runs wait their turn in the device's run queue
	// (one at a time): an LLM routine's concurrency setting. A program's
	// never do.
	Queued bool
	// UsesModel is whether its runs are held back by the LLM usage limit.
	UsesModel bool
	// DryRunsNeedTrust is whether a dry run needs trust too: a program's do,
	// since it runs for real unless it writes.
	DryRunsNeedTrust bool
}

// RoutineConfig is an LLM routine's effective settings: its file's automator.*
// values, overridden by settings (routines.<name>.<field>). Everything that
// needs a routine setting reads it from here.
type RoutineConfig struct {
	RunConfig
	Effort string // "default" leaves it to the backend
	// Folder is the routine's folder setting (home-expanded); empty: none.
	// WorkFolder is Folder; without one, a routine that saves its sessions
	// works in its own folder (Layout.SessionFolder, made when a run needs
	// it), else in an empty temporary one.
	Folder string
	// SaveSession keeps the agent CLI session (in WorkFolder: an agent CLI
	// resumes a session from the folder it ran in).
	SaveSession bool
	// BuiltinTools are the agent CLI built-in tools allowed.
	BuiltinTools []string
	// Params are the routine's parameter values, by name (its prompt inputs).
	Params map[string]any
}

// RoutineConfig resolves an LLM routine's effective settings.
func (a *App) RoutineConfig(r routines.Routine) (RoutineConfig, error) {
	c := RoutineConfig{RunConfig: RunConfig{UsesModel: true}}
	vals, err := (settings.Store{Path: a.Layout.Config(), Extra: r.Settings()}).Values()
	if err != nil {
		return c, err
	}
	key := func(field string) string { return "routines." + r.Name + "." + field }

	c.Enabled = vals.Bool(key("enabled"))
	if c.Timeout, err = vals.Duration(key("timeout")); err != nil {
		return c, err
	}
	c.Effort = vals.String(key("effort"))
	if folder := vals.String(key("folder")); folder != "" {
		if c.Folder, err = settings.ExpandPath(folder); err != nil {
			return c, err
		}
		if !filepath.IsAbs(c.Folder) {
			return c, fmt.Errorf("routines.%s.folder %q: use an absolute path (or one starting with ~/)", r.Name, folder)
		}
	}
	for name := range r.Plugins {
		if p, ok := a.InProcess(name); !ok || !p.InProcess.RoutineSection {
			return c, fmt.Errorf("automator.plugins.%s: the %s plugin takes no settings from routines", name, name)
		}
	}
	c.SaveSession = vals.String(key("session")) == "save"
	c.WorkFolder = c.Folder
	if c.SaveSession && c.Folder == "" {
		c.WorkFolder = a.Layout.SessionFolder(r.Name)
	}
	c.BuiltinTools = vals.List(key("builtinTools"))
	c.Queued = vals.String(key("concurrency")) != "parallel"
	c.Params = map[string]any{}
	for _, p := range r.Params {
		v := vals.Value(key("params." + p.Name))
		if v == nil {
			return c, fmt.Errorf("parameter %q needs a value: "+product.Command+" settings set routines.%s.params.%s <value>", p.Name, r.Name, p.Name)
		}
		c.Params[p.Name] = v
	}
	if !r.Scheduled() {
		return c, nil
	}
	c.Schedule = vals.String(key("schedule"))
	if c.Staleness, err = vals.Duration(key("staleness")); err != nil {
		return c, err
	}
	return c, nil
}
