package engine

import (
	"errors"
	"fmt"
	"os"

	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// Adding a program, a hook or a watcher from its file (and the executable
// beside it): what `routines create --kind program`, `hooks add` and
// `watchers add` do (replace: their update), and what installing a
// template of those kinds does. A new one is off and untrusted.

// DisableNew stores routines.<name>.enabled = false for a routine of
// either kind about to be written (defs are its settings), so it doesn't
// run on its schedule until it's enabled on purpose. It refuses a name
// already taken; undo removes the setting again.
func (a *App) DisableNew(name string, defs []settings.Setting) (undo func(), err error) {
	if why := a.NameTaken(name); why != "" {
		return nil, errors.New(why)
	}
	st := settings.Store{Path: a.Layout.Config(), Extra: defs}
	key := "routines." + name + ".enabled"
	if _, err := st.Set(key, "false"); err != nil {
		return nil, err
	}
	return func() { _, _ = st.Reset(key) }, nil
}

// AddProgram adds the program described by the file at from as name (a
// new one paused), or (replace) updates the one there.
func (a *App) AddProgram(name, from string, replace bool) (programs.Program, error) {
	raw, err := os.ReadFile(from)
	if err != nil {
		return programs.Program{}, err
	}
	parsed, err := programs.Parse(name, string(raw))
	if err != nil {
		return programs.Program{}, err
	}
	if s, ok := a.FindSignIn(name); ok && s.Kind != "program" && len(parsed.Secrets) > 0 {
		return programs.Program{}, fmt.Errorf("%s is already a sign-in's name (`%s auth login %s`): give a program with secrets another name", name, product.Command, name)
	}
	undo := func() {}
	if !replace {
		if undo, err = a.DisableNew(name, parsed.Settings()); err != nil {
			return programs.Program{}, err
		}
	}
	p, err := programs.Add(a.Layout.Programs(), name, from, replace)
	if err != nil {
		undo()
		return programs.Program{}, err
	}
	return p, nil
}

// AddHook adds the hook whose file is at from as name (a new one off), or
// (replace) updates the one there. The event type it listens for must be
// one its watcher declares. An LLM hook's tools are the caller's to check.
func (a *App) AddHook(name, from string, replace bool) (hooks.Hook, error) {
	raw, err := os.ReadFile(from)
	if err != nil {
		return hooks.Hook{}, err
	}
	parsed, err := hooks.Parse(name, from, string(raw))
	if err != nil {
		return hooks.Hook{}, err
	}
	if err := a.CheckOn(parsed.On); err != nil {
		return hooks.Hook{}, err
	}
	if parsed.Program != nil && len(parsed.Program.Secrets) > 0 {
		if s, ok := a.FindSignIn(name); ok && s.Kind != "hook" {
			return hooks.Hook{}, fmt.Errorf("%s is already a sign-in's name (`%s auth login %s`): give a hook with secrets another name", name, product.Command, name)
		}
	}
	undo := func() {}
	if !replace {
		if why := a.NameTaken(name); why != "" {
			return hooks.Hook{}, errors.New(why)
		}
		// Off before it exists, so the daemon never sees it on.
		st := settings.Store{Path: a.Layout.Config(), Extra: parsed.Settings()}
		if _, err := st.Set("hooks."+name+".enabled", "false"); err != nil {
			return hooks.Hook{}, err
		}
		undo = func() { _, _ = st.Reset("hooks." + name + ".enabled") }
	}
	h, err := hooks.Add(a.Layout.Hooks(), name, from, replace)
	if err != nil {
		undo()
		return hooks.Hook{}, err
	}
	return h, nil
}

// AddWatcher adds the watcher whose description file is at from as name,
// or (replace) updates the one there. It runs once trusted.
func (a *App) AddWatcher(name, from string, replace bool) (watchers.Watcher, error) {
	raw, err := os.ReadFile(from)
	if err != nil {
		return watchers.Watcher{}, err
	}
	parsed, err := watchers.Parse(name, string(raw))
	if err != nil {
		return watchers.Watcher{}, err
	}
	if len(parsed.Program.Secrets) > 0 {
		if s, ok := a.FindSignIn(name); ok && s.Kind != "watcher" {
			return watchers.Watcher{}, fmt.Errorf("%s is already a sign-in's name (`%s auth login %s`): give a watcher with secrets another name", name, product.Command, name)
		}
	}
	if !replace {
		if why := a.NameTaken(name); why != "" {
			return watchers.Watcher{}, errors.New(why)
		}
	}
	return watchers.Add(a.Layout.Watchers(), name, from, replace)
}
