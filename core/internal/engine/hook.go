package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// A hook: a prompt or a program run when a watcher prints an event, not on a
// schedule. Its action is a Runnable of either kind (an LLM routine's or a
// program's side, told it runs for events), so everything around a run is
// shared: trust, its turn, keeping awake, stopping, the timeout, run status,
// history and notifications. This file is what a hook adds: what it listens
// for, and what trusting it covers (its trigger too).

// hook is a hook as the engine runs it.
type hook struct {
	h      hooks.Hook
	action Runnable
}

// HookConfig is a hook's effective settings: its action's, and what only a
// hook has.
type HookConfig struct {
	RunConfig
	// Rate is how many runs it may start in any hour.
	Rate  int
	Batch bool
	On    hooks.On
}

// Hooks are every hook, sorted by name, and those that don't load. A name
// a routine or a watcher has too is broken for the hook.
func (a *App) Hooks() ([]Runnable, []Broken, error) {
	set, err := hooks.Load(a.Layout.Hooks())
	if err != nil {
		return nil, nil, err
	}
	partials, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return nil, nil, err
	}
	var out []Runnable
	var broken []Broken
	for _, e := range set.Errors {
		broken = append(broken, Broken{Kind: KindLLM, Name: e.Name, Path: e.Path, Err: e.Err})
	}
	for _, h := range set.Hooks {
		x := a.hookOf(h, partials)
		if why := a.takenElsewhere(h.Name, "hook"); why != "" {
			broken = append(broken, Broken{Kind: x.Kind(), Name: h.Name, Path: h.Path, Err: errors.New(why)})
			continue
		}
		out = append(out, x)
	}
	sort.Slice(broken, func(i, j int) bool { return broken[i].Name < broken[j].Name })
	return out, broken, nil
}

// hookOf is h as a Runnable; set is the routines folder (its partials).
func (a *App) hookOf(h hooks.Hook, set *routines.Set) *hook {
	x := &hook{h: h}
	if h.Prompt != nil {
		if set == nil {
			set = &routines.Set{Partials: map[string]string{}}
		}
		x.action = &llmRoutine{r: *h.Prompt, set: set, event: true}
	} else {
		x.action = &program{p: *h.Program, event: true}
	}
	return x
}

// FindHook is the hook named name.
func (a *App) FindHook(name string) (Runnable, error) {
	all, broken, err := a.Hooks()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for _, x := range all {
		if x.Name() == name {
			return x, nil
		}
		names = append(names, x.Name())
	}
	for _, b := range broken {
		if b.Name == name {
			return nil, b
		}
	}
	if why := a.takenElsewhere(name, "hook"); why != "" {
		return nil, fmt.Errorf("no hook %q: %s", name, why)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no hook %q: there are no hooks", name)
	}
	return nil, fmt.Errorf("no hook %q (have: %s)", name, strings.Join(names, ", "))
}

// FindAny is the routine, program or hook named name: what commands that
// take any of them (pause, resume, trust, stop) find.
func (a *App) FindAny(name string) (Runnable, error) {
	if hooks.Exists(a.Layout.Hooks(), name) {
		return a.FindHook(name)
	}
	return a.Find(name)
}

// AsHook is x as a hook, when it is one.
func AsHook(x Runnable) (hooks.Hook, bool) {
	h, ok := x.(*hook)
	if !ok {
		return hooks.Hook{}, false
	}
	return h.h, true
}

// IsHook reports whether a hook has name.
func (a *App) IsHook(name string) bool { return hooks.Exists(a.Layout.Hooks(), name) }

// HookConfig resolves a hook's effective settings.
func (a *App) HookConfig(x Runnable) (HookConfig, error) {
	h, ok := x.(*hook)
	if !ok {
		return HookConfig{}, fmt.Errorf("%s isn't a hook", x.Name())
	}
	c := HookConfig{Batch: h.h.Batch, On: h.h.On}
	var err error
	if c.RunConfig, err = h.runConfig(a); err != nil {
		return c, err
	}
	vals, err := (settings.Store{Path: a.Layout.Config(), Extra: h.h.Settings()}).Values()
	if err != nil {
		return c, err
	}
	c.Rate = vals.Int(h.h.Key("rate"))
	return c, nil
}

func (h *hook) Name() string                 { return h.h.Name }
func (h *hook) Kind() Kind                   { return h.action.Kind() }
func (h *hook) Path() string                 { return h.h.Path }
func (h *hook) Description() string          { return h.h.Description }
func (h *hook) Source() string               { return h.h.Source }
func (h *hook) Settings() []settings.Setting { return h.h.Settings() }

// runConfig: its action's, with no schedule, and its staleness (how old an
// event may be).
func (h *hook) runConfig(a *App) (RunConfig, error) {
	c, err := h.action.runConfig(a)
	if err != nil {
		return c, err
	}
	vals, err := (settings.Store{Path: a.Layout.Config(), Extra: h.h.Settings()}).Values()
	if err != nil {
		return c, err
	}
	c.Schedule = ""
	c.Staleness, err = vals.Duration(h.h.Key("staleness"))
	return c, err
}

// problems: its action's, and what keeps it from hearing its events (see
// listening).
func (h *hook) problems(a *App) []Problem {
	return append(h.action.problems(a), a.listening(h.h, true)...)
}

// listening is what keeps a hook from hearing its watcher's events: no such
// watcher, one that doesn't load or isn't trusted, and (failing) one whose
// polls fail or whose stream keeps ending.
func (a *App) listening(h hooks.Hook, failing bool) []Problem {
	w := h.On.Watcher
	set, err := watchers.Load(a.Layout.Watchers())
	if err != nil {
		return []Problem{{Text: err.Error()}}
	}
	found, ok := set.Find(w)
	if !ok {
		for _, e := range set.Errors {
			if e.Name == w {
				return []Problem{{Text: fmt.Sprintf("its watcher %s doesn't load: %v", w, e.Err)}}
			}
		}
		return []Problem{{Text: "it listens to " + w + ", which isn't a watcher",
			Fix: product.Command + " watchers add " + w + " --from <folder>/" + watchers.DescriptionFile}}
	}
	why, err := a.WatcherUntrusted(found)
	switch {
	case err != nil:
		return []Problem{{Text: err.Error()}}
	case why != "":
		return []Problem{{Text: "its watcher " + w + " isn't trusted (" + why + ")", Fix: product.Command + " watchers trust " + w}}
	}
	if failing {
		if st, err := watchers.LoadState(a.Layout.WatcherState()); err == nil {
			if ws := st.Watchers[w]; ws != nil && ws.Error != "" {
				return []Problem{{Text: "its watcher " + w + " is failing: " + ws.Error}}
			}
		}
	}
	return nil
}

func (h *hook) check(ctx context.Context, a *App, rd *Readiness) { h.action.check(ctx, a, rd) }

func (h *hook) reaches(a *App) []string { return h.action.reaches(a) }

// approval: its action's, and its trigger: another watcher or event type,
// or batching, needs another review.
func (h *hook) approval(a *App) (trust.Approval, error) {
	ap, err := h.action.approval(a)
	if err != nil {
		return ap, err
	}
	ap.Trigger = &trust.Trigger{Watcher: h.h.On.Watcher, Type: h.h.On.Type, Batch: h.h.Batch}
	return ap, nil
}

func (h *hook) run(ctx context.Context, a *App, rc runContext) (*RunResult, error) {
	if len(occurrenceEvents(rc.occ)) == 0 {
		return &RunResult{Kind: h.Kind()}, errors.New("a hook runs for an event, and this run has none")
	}
	return h.action.run(ctx, a, rc)
}

// MaxBatch is the most events one batch run gets: the latest ones.
const MaxBatch = 100

// EventOccurrence is a hook's run for events, fired by trig (trigger.Event,
// or trigger.Manual for a sample fired by hand): it's for the latest one's
// time, and identified by it.
func EventOccurrence(trig string, evs ...watchers.Event) trigger.Occurrence {
	if len(evs) > MaxBatch {
		evs = evs[len(evs)-MaxBatch:]
	}
	last := evs[len(evs)-1]
	at := last.At
	for _, e := range evs {
		if e.At.After(at) {
			at = e.At
		}
	}
	return trigger.Occurrence{Trigger: trig, ID: trig + ":" + last.Watcher + ":" + last.ID, At: at,
		Data: map[string]any{"events": evs}}
}

// mergeEvents folds a later occurrence's events into one waiting (a hook
// that batches).
func mergeEvents(waiting, next trigger.Occurrence) trigger.Occurrence {
	return EventOccurrence(waiting.Trigger, append(occurrenceEvents(waiting), occurrenceEvents(next)...)...)
}

// occurrenceEvents are the events an occurrence carries (a hook's run's).
func occurrenceEvents(occ trigger.Occurrence) []watchers.Event {
	evs, _ := occ.Data["events"].([]watchers.Event)
	return evs
}

// keepFresh is the occurrence with only its events no older than staleness
// at now (ok false when none is left).
func keepFresh(occ trigger.Occurrence, staleness time.Duration, now time.Time) (trigger.Occurrence, bool) {
	var fresh []watchers.Event
	for _, e := range occurrenceEvents(occ) {
		if now.Sub(e.At) <= staleness {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return occ, false
	}
	return EventOccurrence(occ.Trigger, fresh...), true
}

// Fire runs a hook for an event given by hand (a sample: `hooks fire`), as a
// watcher's would, but now and whatever its staleness and rate.
func (a *App) Fire(ctx context.Context, x Runnable, ev watchers.Event, o RunOptions) (*RunResult, error) {
	if _, ok := x.(*hook); !ok {
		return nil, fmt.Errorf("%s isn't a hook", x.Name())
	}
	if ev.Watcher == "" {
		h, _ := AsHook(x)
		ev.Watcher = h.On.Watcher
	}
	o.Occurrence = EventOccurrence(trigger.Manual, ev)
	return a.Run(ctx, x, o)
}

// takenElsewhere says what else has name ("" when nothing does) besides
// what (routine, hook or watcher): names are unique across routines,
// programs, hooks and watchers, so runs, trust and settings never mix them up.
func (a *App) takenElsewhere(name, what string) string {
	if what != "hook" && hooks.Exists(a.Layout.Hooks(), name) {
		return fmt.Sprintf("a hook named %q already exists", name)
	}
	if what != "watcher" && watchers.Exists(a.Layout.Watchers(), name) {
		return fmt.Sprintf("a watcher named %q already exists", name)
	}
	if what != "routine" {
		if why := a.routineNameTaken(name); why != "" {
			return why
		}
	}
	return ""
}

// WriteBuiltins are the built-in tools among names that can change
// something, on the configured backend (any it doesn't know may).
func (a *App) WriteBuiltins(names []string) []string {
	var set *agent.BuiltinSet
	if def, err := a.BackendDef(); err == nil {
		set = def.Builtins
	}
	var out []string
	for _, n := range names {
		readOnly := false
		if set != nil {
			for _, t := range set.Tools {
				if t.Name == n {
					readOnly = t.ReadOnly
				}
			}
		}
		if !readOnly {
			out = append(out, n)
		}
	}
	return out
}
