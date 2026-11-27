package ui

import (
	"net/url"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// bugReporter is Export Logs… and Report a Bug… for a routine or a hook,
// at the foot of its Overview. Export Logs… has the core write the logs zip
// (`logs`), then opens its folder and says what's in it; Report a Bug… asks
// what happened and has the core open the issue, filled in, on GitHub
// (`report`): the reporter submits it from their own account. Both the
// browser and the file manager are opened by the core for a report.
type bugReporter struct {
	cli   cli.CLI
	win   fyne.Window
	async Async
	name  string
	hook  bool
	// redraw draws the window again; fail shows why export failed, as the
	// window's other controls do ("": clears it).
	redraw func()
	fail   func(string)
	// openURL opens the logs' folder in the file manager.
	openURL func(string) error

	exporting bool
	// report is Report a Bug…'s dialog while it's open.
	report *reportDialog
}

func newBugReporter(c cli.CLI, win fyne.Window, async Async, name string, hook bool, redraw func(), fail func(string)) *bugReporter {
	return &bugReporter{cli: c, win: win, async: async, name: name, hook: hook, redraw: redraw, fail: fail, openURL: openURL}
}

// noun is what it reports on.
func (b *bugReporter) noun() string {
	if b.hook {
		return "hook"
	}
	return "routine"
}

// view is its two buttons.
func (b *bugReporter) view() fyne.CanvasObject {
	export := widget.NewButton("Export Logs…", b.exportLogs)
	if b.exporting {
		export.SetText("Exporting…")
		export.Disable()
	}
	report := widget.NewButton("Report a Bug…", b.startReport)
	export.Importance, report.Importance = widget.LowImportance, widget.LowImportance
	return right(export, report)
}

// exportLogs is Export Logs…: the zip is written, its folder opened, and
// what's in it shown.
func (b *bugReporter) exportLogs() {
	b.exporting = true
	b.redraw()
	b.async(func() func() {
		out, err := b.cli.ExportLogs(b.name, b.hook)
		return func() {
			b.exporting = false
			if err != nil {
				b.fail(err.Error())
				return
			}
			b.fail("")
			b.showExported(out, b.openURL(folderURL(out.Path)))
			b.redraw()
		}
	})
}

// folderURL is the address of the folder a file is in.
func folderURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.Dir(path)}).String()
}

// exportedView is what an export wrote, and that it's to be read before
// it's shared; openErr is why its folder didn't open.
func (b *bugReporter) exportedView(out cli.LogsOutput, openErr error) fyne.CanvasObject {
	where := secondary("Saved in its folder, now open in your file manager:")
	box := container.NewVBox()
	if openErr != nil {
		where = secondary("Saved here:")
		box.Add(errorRow("Couldn't open its folder: " + openErr.Error()))
	}
	runs := "no runs"
	if len(out.Runs) > 0 {
		runs = "its latest " + plural(len(out.Runs), "run", "runs")
	}
	box.Add(where)
	box.Add(mono(out.Path))
	box.Add(secondary("It holds the " + b.noun() + "'s file, " + runs + " and the app's log lines about them. " +
		"Things that look like secrets are redacted, but the runs' text and tool results are kept: read it before sharing it."))
	return fixedWidth(460, box)
}

func (b *bugReporter) showExported(out cli.LogsOutput, openErr error) {
	dialog.NewCustom("Logs Exported", "Done", b.exportedView(out, openErr), b.win).Show()
}

// startReport is Report a Bug…: its dialog.
func (b *bugReporter) startReport() {
	b.report = newReportDialog(b)
	b.report.d.Show()
}

// reportDialog is Report a Bug…'s dialog: a title, what happened, whether
// to attach the logs, and Open on GitHub; why it failed shows in it. Once
// the core has opened the issue it closes, unless there's something left
// to do: drag the logs onto the issue, or open it (the browser didn't).
type reportDialog struct {
	b      *bugReporter
	title  *widget.Entry
	what   *widget.Entry
	attach *widget.Check

	sending bool
	err     string
	// result is the issue the core filled in, while what's left to do shows.
	result *cli.ReportOutput

	body *fyne.Container
	d    *dialog.CustomDialog
}

func newReportDialog(b *bugReporter) *reportDialog {
	r := &reportDialog{b: b, title: widget.NewEntry(), what: widget.NewMultiLineEntry(), body: container.NewVBox()}
	r.title.SetPlaceHolder("What went wrong, in a line")
	r.what.SetPlaceHolder("What happened, and what you expected")
	r.what.Wrapping = fyne.TextWrapWord
	r.what.SetMinRowsVisible(5)
	r.attach = widget.NewCheck("Attach logs", func(bool) { r.render() })
	r.d = dialog.NewCustomWithoutButtons("Report a Bug in "+b.name, fixedWidth(460, r.body), b.win)
	r.d.SetOnClosed(func() {
		if b.report == r {
			b.report = nil
		}
	})
	r.render()
	return r
}

// render shows the dialog's state: its form, or what's left to do.
func (r *reportDialog) render() {
	if r.result != nil {
		r.body.Objects = r.doneView()
		done := widget.NewButton("Done", r.d.Hide)
		done.Importance = widget.HighImportance
		r.d.SetButtons([]fyne.CanvasObject{done})
		r.body.Refresh()
		return
	}
	objects := []fyne.CanvasObject{
		widget.NewLabel("Title"), r.title,
		widget.NewLabel("What happened"), r.what,
		r.attach,
		secondary("The issue is filed from your own GitHub account, and is public if the repository is."),
	}
	if r.attach.Checked {
		objects = append(objects, secondary("The logs may hold the "+r.b.noun()+"'s text and tool results (things that look like secrets are redacted): read them before attaching them."))
	}
	if r.err != "" {
		objects = append(objects, errorRow(r.err))
	}
	r.body.Objects = objects
	r.body.Refresh()
	cancel := widget.NewButton("Cancel", r.d.Hide)
	open := widget.NewButton("Open on GitHub", r.send)
	open.Importance = widget.HighImportance
	if r.sending {
		open.SetText("Opening…")
		open.Disable()
		r.title.Disable()
		r.what.Disable()
		r.attach.Disable()
	} else {
		r.title.Enable()
		r.what.Enable()
		r.attach.Enable()
	}
	r.d.SetButtons([]fyne.CanvasObject{cancel, open})
}

// doneView is what's left once the core opened the issue: open it, if the
// browser didn't; drag the logs onto it.
func (r *reportDialog) doneView() []fyne.CanvasObject {
	res := r.result
	var objects []fyne.CanvasObject
	if res.Opened {
		objects = append(objects, text("The issue is open in your browser: read it, then submit it."))
	} else {
		u, err := url.Parse(res.URL)
		objects = append(objects, text("Your browser didn't open: open the issue here, read it, then submit it."))
		if err == nil {
			objects = append(objects, widget.NewHyperlink("New issue on "+res.Repo, u))
		}
	}
	if res.Logs != nil {
		objects = append(objects, text("Drag "+filepath.Base(res.Logs.Path)+" onto the issue before submitting it: it's in the folder that opened."),
			mono(res.Logs.Path))
	}
	return objects
}

// send is Open on GitHub: the core fills in the issue and opens it.
func (r *reportDialog) send() {
	r.sending, r.err = true, ""
	r.render()
	title, what, attach := r.title.Text, r.what.Text, r.attach.Checked
	r.b.async(func() func() {
		res, err := r.b.cli.ReportBug(r.b.name, r.b.hook, title, what, attach)
		return func() {
			r.sending = false
			switch {
			case err != nil:
				r.err = err.Error()
			case res.Opened && res.Logs == nil:
				r.d.Hide()
				return
			default:
				r.result = &res
			}
			r.render()
		}
	})
}
