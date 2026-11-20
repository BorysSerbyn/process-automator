package engine

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// RoutineConfig is a routine's effective settings: its file's automator.* values,
// overridden by settings (routines.<name>.<field>). Everything that needs a
// routine setting reads it from here.
type RoutineConfig struct {
	Enabled   bool
	Schedule  string        // empty: runs only by hand
	Staleness time.Duration // scheduled routines only
	Timeout   time.Duration
	Effort    string // "default" leaves it to the backend
	// Folder is where the run works (home-expanded); empty: a temporary folder.
	Folder string
	// SaveSession keeps the agent CLI session (in Folder).
	SaveSession bool
	// BuiltinTools are the agent CLI built-in tools allowed.
	BuiltinTools []string
	// Parallel runs alongside other runs instead of in the run queue.
	Parallel bool
	// OnClick is the routine's click action for its notifications ("" for
	// notifications.on-click).
	OnClick string
	// Params are the routine's parameter values, by name (its prompt inputs).
	Params map[string]any
}

// RoutineConfig resolves a routine's effective settings.
func (a *App) RoutineConfig(r routines.Routine) (RoutineConfig, error) {
	var c RoutineConfig
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
		if p, ok := a.FindPlugin(name); !ok || p.Serve == nil || !p.RoutineSection {
			return c, fmt.Errorf("automator.plugins.%s: the %s plugin takes no settings from routines", name, name)
		}
	}
	c.SaveSession = vals.String(key("session")) == "save"
	if c.SaveSession && c.Folder == "" {
		return c, fmt.Errorf("routines.%s.session save needs a folder (a saved session belongs to the folder it ran in): set routines.%s.folder", r.Name, r.Name)
	}
	c.BuiltinTools = vals.List(key("builtinTools"))
	c.Parallel = vals.String(key("concurrency")) == "parallel"
	c.OnClick = vals.String(key("onNotificationClick"))
	c.Params = map[string]any{}
	for _, p := range r.Params {
		v := vals.Value(key("params." + p.Name))
		if v == nil {
			return c, fmt.Errorf("parameter %q needs a value: procauto settings set routines.%s.params.%s <value>", p.Name, r.Name, p.Name)
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
