package ui

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// A program's own parts of its window, as automator-ui's have them: what it
// runs, what a dry run does with it, a run's exit code and output, and its
// description file. Words for what the core says; nothing worked out.

// dryRunNote is what Dry Run does with a program, from whether it writes,
// and that it needs trust as a real run does.
func dryRunNote(p cli.ProgramInfo) string {
	if p.Writes {
		return "Dry Run passes it --dry-run: it's told to change nothing. Like a real run, a dry run needs it trusted."
	}
	return "It doesn't write: Dry Run runs it as it is. Like a real run, a dry run needs it trusted."
}

// shortChecksum is the start of a SHA-256, enough to tell copies apart (Copy
// copies all of it).
func shortChecksum(sum string) string {
	if len(sum) > 12 {
		return sum[:12] + "…"
	}
	return sum
}

// mono is a selectable monospace label that breaks anywhere: a path, a
// file's text, what a program printed.
func mono(s string) *widget.Label {
	l := widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	l.Wrapping = fyne.TextWrapBreak
	l.Selectable = true
	return l
}

// programRows are what a program runs, as trust approves it: its
// executable (the app's copy, by checksum: shortened, copied whole), whether
// it writes, its arguments, and its secrets (each with the variable it's
// passed as and whether it's stored), with signIn under them when given.
func programRows(p cli.ProgramInfo, signIn fyne.CanvasObject) []fyne.CanvasObject {
	return executableRows(p, signIn, true)
}

// executableRows are programRows, saying whether it writes only when
// writes is true (a watcher only reads: it doesn't).
func executableRows(p cli.ProgramInfo, signIn fyne.CanvasObject, writes bool) []fyne.CanvasObject {
	exeRows := []fyne.CanvasObject{mono(p.Executable)}
	if p.Checksum != "" {
		sum := p.Checksum
		cp := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
			fyne.CurrentApp().Clipboard().SetContent(sum)
		})
		cp.Importance = widget.LowImportance
		exeRows = append(exeRows, container.NewHBox(secondary("SHA-256 "+shortChecksum(sum)), cp))
	}
	objects := []fyne.CanvasObject{padded(6, 0, 0, 0, infoRow("Executable", exeRows...))}
	if writes {
		w := "No: a dry run runs it as it is"
		if p.Writes {
			w = "Yes: a dry run passes it --dry-run"
		}
		objects = append(objects, infoRow("Writes", text(w)))
	}
	if len(p.Args) > 0 {
		objects = append(objects, infoRow("Arguments", mono(strings.Join(p.Args, " "))))
	}
	if len(p.Secrets) > 0 {
		var secrets []fyne.CanvasObject
		for _, s := range p.Secrets {
			state := tag("Stored", successColour)
			if !s.Stored {
				state = tag("Not stored", theme.ColorNameWarning)
			}
			name := widget.NewLabelWithStyle(s.Name, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
			secrets = append(secrets, container.NewHBox(name, quiet("as $"+s.Env), state))
		}
		if signIn != nil {
			secrets = append(secrets, signIn)
		}
		objects = append(objects, infoRow("Secrets", secrets...))
	}
	return objects
}

// needsSignIn is whether a secret the program declares isn't stored.
func needsSignIn(p *cli.ProgramInfo) bool {
	if p == nil {
		return false
	}
	for _, s := range p.Secrets {
		if !s.Stored {
			return true
		}
	}
	return false
}

// outputLines is how many lines of output show before it scrolls in a box
// of its own.
const outputLines = 24

// programRun is what a program did in a run: its exit code, whether it was
// a dry run, when it finished, and what it printed (monospace; long output
// scrolls), saying so when the core kept only its start and end.
func programRun(p cli.ProgramRun, finished *time.Time, now time.Time) fyne.CanvasObject {
	tags := container.NewHBox(tag("exit "+itoa(p.ExitCode), ""))
	if p.DryRun {
		tags.Add(tag("dry run", theme.ColorNamePrimary))
	}
	var at fyne.CanvasObject = container.NewHBox()
	if finished != nil {
		at = quiet("Finished " + when(*finished, now))
	}
	box := container.NewVBox(container.NewBorder(nil, nil, tags, at))
	if p.Output == "" {
		return container.NewVBox(box, quiet("It printed nothing."))
	}
	out := padded(4, 4, 6, 6, mono(p.Output))
	if strings.Count(p.Output, "\n") >= outputLines {
		scroll := container.NewVScroll(out)
		scroll.SetMinSize(fyne.NewSize(0, 320))
		out = scroll
	}
	box.Add(card(true, out))
	if p.OutputTruncated {
		box.Add(secondary("It printed more than is kept: this is its start and its end, with what was left out marked between them."))
	}
	return box
}

// programFile is a program's description file, as the app keeps it: what it
// says it does, where it is, its data folder, and its text (read-only: a
// program is updated from a terminal, with command).
func programFile(d cli.RoutineDetail, command string) fyne.CanvasObject {
	return descriptionFile(sourceFile{title: "Program file", description: d.Description, path: d.Path, folder: d.Folder,
		source: d.Source, update: command + " routines update"})
}

// sourceFile is a file the app keeps a copy of (a program's, a hook's, a
// watcher's), and the command that updates it.
type sourceFile struct {
	title, description, path, folder, source, update string
}

// descriptionFile is a file as the app keeps it: what it says it does,
// where it is, its data folder, and its text (read-only: it's updated from
// a terminal).
func descriptionFile(f sourceFile) fyne.CanvasObject {
	objects := []fyne.CanvasObject{cardTitle(f.title), divider()}
	if f.description != "" {
		objects = append(objects, infoRow("Description", text(f.description)))
	}
	objects = append(objects, infoRow("File", mono(f.path)))
	if f.folder != "" {
		objects = append(objects, infoRow("Data folder", mono(f.folder),
			secondary("Where it works: kept between runs, and when it's deleted.")))
	}
	objects = append(objects,
		padded(4, 0, rowInset-8, rowInset-8, card(true, padded(4, 4, 6, 6, mono(f.source)))),
		padded(0, 8, rowInset-8, rowInset-8, rich("Read-only: the app's copy. Update it from a terminal (`"+f.update+"`).")))
	return card(false, objects...)
}
