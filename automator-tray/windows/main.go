// automator-windows is the windows of an app built on process-automator on
// Linux, automator-ui's windows' counterpart: the tray starts it for one
// window (`automator-windows setup`), and it exits when that window closes.
// Like the tray, it acts only through the app's CLI, and its names come
// from the package's install.env. It's a program of its own so that the
// tray, always running, stays small and needs no cgo: Fyne (OpenGL, X11 and
// Wayland) is here, loaded only while a window shows.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/app"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/instance"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/ui"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("automator-windows: ")
	dir := flag.String("app", "", "the app's share folder, holding install.env (default: found beside this program)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: automator-windows [--app <folder>] setup|settings")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*dir, tray.WindowKind(flag.Arg(0))); err != nil {
		if errors.Is(err, instance.ErrHeld) {
			return // the window that's open came to the front
		}
		log.Fatal(err)
	}
}

// content is a window's content: what it shows, and reading it again.
type content interface {
	Content() fyne.CanvasObject
	Load()
}

func run(dir string, kind tray.WindowKind) error {
	if kind != tray.SetupWindow && kind != tray.SettingsWindow {
		return fmt.Errorf("no %q window (setup, settings)", kind)
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	info, err := app.Discover(exe, dir)
	if err != nil {
		return err
	}
	id := info.BundleID
	if id == "" {
		id = info.Command
	}
	holder, err := instance.Claim(instance.Dir(os.Getenv, id), "window-"+string(kind))
	if err != nil {
		return err
	}
	defer holder.Release()

	c := cli.CLI{Path: info.CLI, Command: info.Command}
	a := fyneapp.NewWithID(id + ".windows")
	var w fyne.Window
	var view content
	switch kind {
	case tray.SetupWindow:
		w = a.NewWindow(info.Name + " Setup")
		view = ui.NewSetup(c, w, ui.Background, func(routine string) {
			// The routine's window is the tray's to open (for now, its
			// CLI command in a terminal).
			if err := tray.OpenWindow(info.CLI, tray.Window{Kind: tray.RoutineWindow, Routine: routine}); err != nil {
				log.Print(err)
			}
		})
	default:
		w = a.NewWindow(info.Name + " Settings")
		s := ui.NewSettings(c, w, ui.Background, info.Name)
		// While it shows, read usage again now and then.
		go func() {
			for range time.Tick(ui.UsageEvery) {
				fyne.Do(s.LoadUsage)
			}
		}()
		view = s
	}
	w.SetMaster() // closing it ends the process
	w.SetContent(view.Content())
	w.Resize(fyne.NewSize(640, 680))
	w.CenterOnScreen()
	// Asked for again: to the front, and read again (automator-ui's show()).
	go func() {
		for range holder.Raised {
			fyne.Do(func() {
				w.Show()
				w.RequestFocus()
				view.Load()
			})
		}
	}()
	// What the CLI answers waits for the app's loop, which starts here.
	view.Load()
	w.ShowAndRun()
	return nil
}
