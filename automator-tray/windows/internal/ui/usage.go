package ui

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// usageView is LLM usage against its limit, as `usage` reports it for the
// backend in use: on a Claude plan (plan), the plan's usage in each window
// against llm.usage-limit, with when it resets; with an API key (budget),
// spend against each budget; on a backend with no limit in the app (none),
// that. The core decides what's over; the bars only draw its numbers
// (automator-ui's UsageView).
func usageView(r *cli.Report, problem string, now time.Time) []fyne.CanvasObject {
	limit := fyne.CanvasObject(container.NewHBox())
	if r != nil && r.Kind == "plan" && r.LimitPercent != nil {
		limit = secondary("Limit: " + number(*r.LimitPercent) + "% of the plan")
	}
	title := heading("LLM usage", true)
	objects := []fyne.CanvasObject{
		padded(6, 6, rowInset-8, rowInset-8, container.NewBorder(nil, nil,
			widget.NewIcon(primaryIcon("gauge")), container.NewCenter(limit), title)),
		divider(),
	}
	inset := func(o fyne.CanvasObject) fyne.CanvasObject { return padded(6, 6, rowInset-8, rowInset-8, o) }
	switch {
	case r != nil:
		switch r.Kind {
		case "plan":
			objects = append(objects, planUsage(r, now, inset)...)
		case "budget":
			for _, w := range r.Windows {
				objects = append(objects, budgetRow(w))
			}
		default:
			objects = append(objects, inset(secondary("No usage limit with this backend: its usage can't be read, and its runs have no cost.")))
		}
		if r.Over && r.Reason != "" {
			objects = append(objects, inset(errorRow(r.Reason)))
		}
	case problem != "":
		objects = append(objects, inset(errorRow(problem)))
	default:
		objects = append(objects, inset(container.NewHBox(spinner(), secondary("Reading usage…"))))
	}
	return objects
}

// planUsage is the plan's windows (or, before any reading, "Not read yet"),
// single models' weekly usage, when it was read, and why the last read failed.
func planUsage(r *cli.Report, now time.Time, inset func(fyne.CanvasObject) fyne.CanvasObject) []fyne.CanvasObject {
	known := r.FetchedAt != nil || r.RecordedAt != nil
	for _, w := range r.Windows {
		known = known || w.UsedPercent != nil
	}
	var objects []fyne.CanvasObject
	if known {
		for _, w := range r.Windows {
			objects = append(objects, planRow(w, r.LimitPercent, now))
		}
	} else {
		objects = append(objects, inset(secondary("Not read yet.")))
	}
	var notes []fyne.CanvasObject
	for _, m := range r.Models {
		notes = append(notes, secondary(m.Model+" weekly: "+percent(m.UsedPercent)+" used, resets "+when(m.ResetsAt, now)))
	}
	if len(r.Models) > 0 {
		notes = append(notes, secondary("Single-model limits are shown only; runs don't stop on them."))
	}
	// As of the newer of the last read and the last status line recording.
	var latest *time.Time
	for _, t := range []*time.Time{r.FetchedAt, r.RecordedAt} {
		if t != nil && (latest == nil || t.After(*latest)) {
			latest = t
		}
	}
	if latest != nil {
		notes = append(notes, secondary("As of "+when(*latest, now)+"."))
	}
	if r.ReadError != "" {
		notes = append(notes, secondary("Couldn't read usage just now: "+r.ReadError))
	}
	if len(notes) > 0 {
		objects = append(objects, padded(0, 8, rowInset+114, rowInset, container.NewVBox(notes...)))
	}
	return objects
}

// windowLabel is a window's name as the core gives it, with its length: "Session (5h)".
func windowLabel(w cli.UsageWindow) string {
	name := w.Name
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return name + " (" + w.Length + ")"
}

// planRow is one plan window: how much of the plan is used, against the
// usage limit (the tick), and when it resets; or "Not known".
func planRow(w cli.UsageWindow, limit *float64, now time.Time) fyne.CanvasObject {
	amount := widget.NewLabel("Not known")
	amount.Importance = widget.LowImportance
	var share *float64
	if w.UsedPercent != nil {
		amount.SetText(percent(*w.UsedPercent) + " used")
		amount.Importance = widget.MediumImportance
		v := *w.UsedPercent / 100
		share = &v
	}
	if w.Over {
		amount.Importance = widget.DangerImportance
	}
	var tick *float64
	if limit != nil {
		v := *limit / 100
		tick = &v
	}
	caption := ""
	if w.ResetsAt != nil {
		caption = "Resets " + when(*w.ResetsAt, now) + "."
	}
	return usageRow(windowLabel(w), newUsageBar(share, tick, w.Over), amount, caption)
}

// budgetRow is one budget window: what runs spent against its budget.
func budgetRow(w cli.UsageWindow) fyne.CanvasObject {
	spent, budget := 0.0, 0.0
	if w.SpentUSD != nil {
		spent = *w.SpentUSD
	}
	if w.BudgetUSD != nil {
		budget = *w.BudgetUSD
	}
	amount := widget.NewLabel(cost(spent))
	var share *float64
	caption := "No budget: spend here never stops runs."
	if budget > 0 {
		amount.SetText(cost(spent) + " of " + cost(budget))
		v := spent / budget
		share, caption = &v, ""
	}
	if w.Over {
		amount.Importance = widget.DangerImportance
	}
	return usageRow(windowLabel(w), newUsageBar(share, nil, w.Over), amount, caption)
}

// usageRow is a label, a bar and an amount on a line, a caption under them.
func usageRow(label string, bar fyne.CanvasObject, amount *widget.Label, caption string) fyne.CanvasObject {
	amount.Alignment = fyne.TextAlignTrailing
	amount.TextStyle = fyne.TextStyle{Monospace: true}
	line := container.NewBorder(nil, nil, fixedWidth(110, widget.NewLabel(label)), fixedWidth(160, amount), bar)
	box := container.NewVBox(line)
	if caption != "" {
		box.Add(padded(-8, 0, 114, 0, secondary(caption)))
	}
	return padded(2, 2, rowInset-8, rowInset-8, box)
}

// usageBar is a share of a limit as a bar (empty when not known), with an
// optional tick at the limit.
type usageBar struct {
	widget.BaseWidget
	share, tick *float64
	over        bool
}

func newUsageBar(share, tick *float64, over bool) *usageBar {
	b := &usageBar{share: share, tick: tick, over: over}
	b.ExtendBaseWidget(b)
	return b
}

func (b *usageBar) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(tint(theme.ColorNameForeground, 20))
	if b.share == nil {
		track.FillColor = tint(theme.ColorNameForeground, 13)
	}
	track.CornerRadius = 4
	fill := canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	if b.over {
		fill.FillColor = theme.Color(theme.ColorNameError)
	}
	fill.CornerRadius = 4
	mark := canvas.NewRectangle(tint(theme.ColorNameForeground, 128))
	if b.tick == nil {
		mark.FillColor = color.Transparent
	}
	return &usageBarRenderer{bar: b, track: track, fill: fill, mark: mark}
}

type usageBarRenderer struct {
	bar               *usageBar
	track, fill, mark *canvas.Rectangle
}

func clamp(v float64) float32 { return float32(min(max(v, 0), 1)) }

func (r *usageBarRenderer) Layout(size fyne.Size) {
	y := (size.Height - 8) / 2
	r.track.Move(fyne.NewPos(0, y))
	r.track.Resize(fyne.NewSize(size.Width, 8))
	w := float32(0)
	if r.bar.share != nil {
		w = clamp(*r.bar.share) * size.Width
		if *r.bar.share > 0 {
			w = max(w, 6)
		}
	}
	r.fill.Move(fyne.NewPos(0, y))
	r.fill.Resize(fyne.NewSize(w, 8))
	if r.bar.tick != nil {
		r.mark.Move(fyne.NewPos(clamp(*r.bar.tick)*size.Width-1, (size.Height-14)/2))
		r.mark.Resize(fyne.NewSize(2, 14))
	}
}

func (r *usageBarRenderer) MinSize() fyne.Size { return fyne.NewSize(60, 14) }
func (r *usageBarRenderer) Refresh()           { r.Layout(r.bar.Size()) }
func (r *usageBarRenderer) Destroy()           {}
func (r *usageBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill, r.mark}
}
