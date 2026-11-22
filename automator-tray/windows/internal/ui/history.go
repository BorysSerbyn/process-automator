package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// The parts a routine's window and a hook's share: the run history (each
// run opening to what it did; a hook's, to the events it ran for) and the
// memories (an LLM routine's or LLM hook's).

// runHistory is a window's Runs tab: its latest runs, newest first, each
// opening to what it did, read from `runs show`.
type runHistory struct {
	cli   cli.CLI
	async Async
	name  string
	// now, redraw and failed are the window's: its clock, drawing it
	// again, and saying why something failed.
	now    func() time.Time
	redraw func()
	failed func(string)

	runs         []cli.RunSummary
	loaded       bool
	err          string
	entries      map[string]cli.RunEntry
	runErrors    map[string]string
	openRuns     map[string]bool
	openedLatest bool
}

func newRunHistory(c cli.CLI, async Async, name string, now func() time.Time, redraw func(), failed func(string)) *runHistory {
	return &runHistory{cli: c, async: async, name: name, now: now, redraw: redraw, failed: failed,
		entries: map[string]cli.RunEntry{}, runErrors: map[string]string{}, openRuns: map[string]bool{}}
}

// load reads the runs again; the latest starts open the first time.
func (h *runHistory) load() {
	h.async(func() func() {
		list, err := h.cli.Runs(h.name, RunsShown)
		return func() {
			if err != nil {
				h.err = err.Error()
			} else {
				h.runs, h.err = list, ""
				// The latest run starts open: its output is what's usually wanted.
				if !h.openedLatest && len(list) > 0 {
					h.openedLatest = true
					h.open(list[0].ID)
				}
			}
			h.loaded = true
			h.redraw()
		}
	})
}

func (h *runHistory) toggle(id string) {
	if h.openRuns[id] {
		delete(h.openRuns, id)
		h.redraw()
		return
	}
	h.open(id)
	h.redraw()
}

// open opens a run, reading it in full the first time.
func (h *runHistory) open(id string) {
	h.openRuns[id] = true
	if _, ok := h.entries[id]; ok {
		return
	}
	h.async(func() func() {
		e, err := h.cli.Run(id)
		return func() {
			if err != nil {
				h.runErrors[id] = err.Error()
			} else {
				h.entries[id] = e
				delete(h.runErrors, id)
			}
			h.redraw()
		}
	})
}

// openReport opens a readable report of a run (the core writes and opens it).
func (h *runHistory) openReport(id string) {
	h.async(func() func() {
		err := h.cli.OpenRun(id)
		return func() {
			if err != nil {
				h.failed(err.Error())
			}
		}
	})
}

// view is the Runs tab: newest first, each opening to what it did.
func (h *runHistory) view() fyne.CanvasObject {
	count := fyne.CanvasObject(container.NewHBox())
	if len(h.runs) > 0 {
		count = quiet(plural(len(h.runs), "run", "runs"))
	}
	objects := []fyne.CanvasObject{cardTitle("Runs", count)}
	switch {
	case h.err != "":
		objects = append(objects, padded(0, 10, rowInset, rowInset, errorRow(h.err)))
	case !h.loaded:
		objects = append(objects, padded(8, 16, 0, 0, spinner()))
	case len(h.runs) == 0:
		objects = append(objects, divider(), padded(20, 20, 0, 0, container.NewCenter(quiet("It hasn't run yet."))))
	}
	for _, run := range h.runs {
		objects = append(objects, divider(), h.row(run))
		if h.openRuns[run.ID] {
			objects = append(objects, padded(0, 14, 34, rowInset, h.detail(run.ID)))
		}
	}
	return card(false, objects...)
}

// row is one run: when, how it ended, what started it, why; how many tool
// calls it made and what it cost.
func (h *runHistory) row(run cli.RunSummary) fyne.CanvasObject {
	line := container.NewHBox(widget.NewLabelWithStyle(when(run.StartedAt, h.now()), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		outcomeTag(run.Outcome))
	if run.Trigger != "" {
		line.Add(tag(run.Trigger, ""))
	}
	left := container.NewVBox(line)
	if run.Reason != "" {
		reason := quiet(run.Reason)
		if !h.openRuns[run.ID] {
			reason.Truncation = fyne.TextTruncateEllipsis
		} else {
			reason.Wrapping = fyne.TextWrapWord
		}
		left.Add(reason)
	}
	numbers := container.NewVBox()
	if run.ToolCalls != nil {
		numbers.Add(quiet(plural(*run.ToolCalls, "tool call", "tool calls")))
	}
	if run.CostUSD != nil {
		numbers.Add(quiet(cost(*run.CostUSD)))
	}
	id := run.ID
	return newDisclosure(h.openRuns[id], false, func() { h.toggle(id) }, container.NewBorder(nil, nil, nil, numbers, left))
}

// detail is what a run did: a hook's events, its report (done, nothing to
// do), its final text, every tool call (writes tinted) and its tokens; a
// program's summary, exit code and output; Open Report.
func (h *runHistory) detail(id string) fyne.CanvasObject {
	if msg, ok := h.runErrors[id]; ok {
		return errorRow(msg)
	}
	e, ok := h.entries[id]
	if !ok {
		return spinner()
	}
	box := container.NewVBox()
	if len(e.Events) > 0 {
		box.Add(events(e.Events, h.now()))
	}
	report := widget.NewButton("Open Report", func() { h.openReport(id) })
	if res := e.Result; res != nil {
		tags := container.NewHBox()
		if res.Outcome != "" {
			tags.Add(tag(res.Outcome, ""))
		}
		if res.DryRun {
			tags.Add(tag("dry run", theme.ColorNamePrimary))
		}
		if len(tags.Objects) > 0 {
			box.Add(tags)
		}
		if res.Text != "" {
			out := text(res.Text)
			out.Selectable = true
			box.Add(card(true, padded(4, 4, 6, 6, out)))
		}
		box.Add(toolCalls(res))
		tokens := fmt.Sprintf("%d in · %d out · %d cached · %s", res.InputTokens, res.OutputTokens, res.CachedTokens, res.Backend)
		box.Add(container.NewBorder(nil, nil, quiet(tokens), report))
	} else {
		if t := firstNonEmpty(e.Reason, e.Error); t != "" {
			box.Add(text(t))
		}
		if p := e.Program; p != nil {
			box.Add(programRun(*p, e.FinishedAt, h.now()))
		}
		box.Add(right(report))
	}
	return box
}

// events are the events a hook's run was for: each one's type, when it
// happened, its watcher, and its data. Events are outside data: their text
// shows as is, in monospace, never read as markup.
func events(list []cli.Event, now time.Time) fyne.CanvasObject {
	title := "For this event"
	if len(list) > 1 {
		title = "For these " + itoa(len(list)) + " events"
	}
	var rows []fyne.CanvasObject
	for i, e := range list {
		if i > 0 {
			rows = append(rows, divider())
		}
		about := []string{when(e.At, now)}
		if e.Watcher != "" {
			about = append(about, "from "+e.Watcher)
		}
		about = append(about, e.ID)
		head := container.NewHBox(widget.NewLabelWithStyle(e.Type, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true}),
			quiet(strings.Join(about, " · ")))
		row := container.NewVBox(head)
		if data := e.DataText(); data != "" {
			d := mono(data)
			d.SizeName = captionSize
			row.Add(padded(-8, 0, 0, 0, d))
		}
		rows = append(rows, padded(2, 2, 6, 6, row))
	}
	return container.NewVBox(quiet(title), card(true, rows...))
}

// memoryList is a window's Memories tab: what an LLM routine's (or LLM
// hook's) runs are given to remember, added and removed here.
type memoryList struct {
	cli    cli.CLI
	async  Async
	name   string
	redraw func()

	memories []cli.Memory
	loaded   bool
	err      string
	draft    string
}

func newMemoryList(c cli.CLI, async Async, name string, redraw func()) *memoryList {
	return &memoryList{cli: c, async: async, name: name, redraw: redraw}
}

func (m *memoryList) load() {
	m.async(func() func() {
		list, err := m.cli.Memories(m.name)
		return func() {
			if err != nil {
				m.err = err.Error()
			} else {
				m.memories = list
			}
			m.loaded = true
			m.redraw()
		}
	})
}

func (m *memoryList) add(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.async(func() func() {
		err := m.cli.AddMemory(m.name, text)
		return func() {
			if err != nil {
				m.err = err.Error()
			} else {
				m.err, m.draft = "", ""
			}
			m.load()
		}
	})
}

func (m *memoryList) remove(id string) {
	m.async(func() func() {
		err := m.cli.RemoveMemory(id)
		return func() {
			m.err = ""
			if err != nil {
				m.err = err.Error()
			}
			m.load()
		}
	})
}

// view is the Memories tab.
func (m *memoryList) view() fyne.CanvasObject {
	count := fyne.CanvasObject(container.NewHBox())
	if len(m.memories) > 0 {
		count = quiet(plural(len(m.memories), "memory", "memories"))
	}
	objects := []fyne.CanvasObject{cardTitle("Memories", count),
		padded(0, 6, rowInset-8, rowInset, secondary("What "+m.name+"'s runs are given to remember, besides the ones every routine gets."))}
	if m.loaded && len(m.memories) == 0 {
		objects = append(objects, divider(), padded(16, 16, 0, 0, container.NewCenter(quiet("No memories yet."))))
	}
	for _, mem := range m.memories {
		id := mem.ID
		remove := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { m.remove(id) })
		remove.Importance = widget.LowImportance
		t := text(mem.Text)
		t.Selectable = true
		objects = append(objects, divider(), padded(2, 2, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, container.NewCenter(remove), t)))
	}
	field := widget.NewEntry()
	field.SetPlaceHolder("Something " + m.name + " should remember")
	field.SetText(m.draft)
	field.OnChanged = func(s string) { m.draft = s }
	field.OnSubmitted = m.add
	add := widget.NewButton("Add", func() { m.add(field.Text) })
	objects = append(objects, divider(), padded(8, 8, rowInset-8, rowInset-8, container.NewBorder(nil, nil, nil, add, field)))
	if m.err != "" {
		objects = append(objects, padded(0, 10, rowInset, rowInset, errorRow(m.err)))
	}
	return card(false, objects...)
}
