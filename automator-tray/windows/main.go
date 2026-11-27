// automator-windows is the window of an app built on process-automator on
// Linux, automator-ui's window's counterpart: the tray starts it for one of
// its screens (`automator-windows setup`, `automator-windows routine standup`,
// `automator-windows hook open-meet run r-42`), which it shows in the app's
// one window; started again while that window is open, it replaces it: the
// open one quits, and a new window opens on the screen, in front (one that
// was minimized, on another desktop or off-screen, would take the screen
// where I can't see it). It exits when the window closes.
// Like the tray, it acts only through the app's CLI, and its names come
// from the package's install.env. It's a program of its own so that the
// tray, always running, stays small and needs no cgo: Fyne (OpenGL, X11 and
// Wayland) is here, loaded only while a window shows.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/app"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/instance"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/shell"
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/ui"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("automator-windows: ")
	dir := flag.String("app", "", "the app's share folder, holding install.env (default: found beside this program)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: automator-windows [--app <folder>] setup|settings|routines|routine <name> [review|run <id>]|hooks|hook <name> [review|run <id>]|watcher <name>|acknowledgements|updates|template-updates")
		flag.PrintDefaults()
	}
	flag.Parse()
	w, err := window(flag.Args())
	if err != nil {
		log.Print(err)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*dir, w); err != nil {
		log.Fatal(err)
	}
}

// StatusEvery is how often the window reads where updates stand, for its
// sidebar.
const StatusEvery = time.Minute

// Size is the window's size when it opens: always the same, centred.
var Size = fyne.NewSize(960, 760)

// window is the screen the arguments name.
func window(args []string) (tray.Window, error) {
	if len(args) == 0 {
		return tray.Window{}, errors.New("which window?")
	}
	w := tray.Window{Kind: tray.WindowKind(args[0])}
	switch w.Kind {
	case tray.SetupWindow, tray.SettingsWindow, tray.RoutinesWindow, tray.HooksWindow, tray.AcknowledgementsWindow, tray.UpdatesWindow,
		tray.TemplateUpdatesWindow:
		if len(args) == 1 {
			return w, nil
		}
	case tray.RoutineWindow, tray.HookWindow, tray.WatcherWindow:
		switch {
		case len(args) == 3 && args[2] == "review" && w.Kind != tray.WatcherWindow:
			w.Review, args = true, args[:2]
		case len(args) == 4 && args[2] == "run" && args[3] != "" && w.Kind != tray.WatcherWindow:
			w.Run, args = args[3], args[:2]
		}
		if len(args) == 2 && args[1] != "" {
			w.Routine = args[1]
			return w, nil
		}
		return w, fmt.Errorf("which %s?", w.Kind)
	default:
		return w, fmt.Errorf("no %q window", args[0])
	}
	return w, fmt.Errorf("the %s window takes no arguments", w.Kind)
}

// lockName is the window's lock: the app has one window.
const lockName = "window"

// ReplaceWait is how long the window that's open is given to quit, before
// it's ended, and then to be gone.
var ReplaceWait = 3 * time.Second

// asked is the screen a later start asked for, if it names one: the
// screen's arguments as one line of JSON, as a windows app from before
// replacing asks it.
func asked(line string) (tray.Window, bool) {
	var args []string
	if json.Unmarshal([]byte(line), &args) != nil {
		return tray.Window{}, false
	}
	w, err := window(args)
	return w, err == nil
}

func run(dir string, which tray.Window) error {
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
	holder, err := instance.Replace(instance.Dir(os.Getenv, id), lockName, ReplaceWait)
	if err != nil {
		return err
	}
	defer holder.Release()

	c := cli.CLI{Path: info.CLI, Command: info.Command}
	ui.AppName = info.Name
	a := fyneapp.NewWithID(id + ".windows")
	a.Settings().SetTheme(ui.Theme())
	w := a.NewWindow(info.Name)
	sh := shell.New(c, w, ui.Background, info.Name)
	// Run status, as it changes, for the screens that follow it and the
	// sidebar's counts.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var last *cli.RunStatus // the UI's thread's
	go c.Follow(ctx, func(s *cli.RunStatus) {
		fyne.Do(func() {
			last = s
			sh.Update(s)
		})
	})
	// Shown again now and then, so one past its validUntil shows as not
	// responding; the plan's usage read again while Settings shows.
	go func() {
		for range time.Tick(15 * time.Second) {
			fyne.Do(func() { sh.Update(last) })
		}
	}()
	go func() {
		for range time.Tick(ui.UsageEvery) {
			fyne.Do(sh.LoadUsage)
		}
	}()
	w.SetMaster() // closing it ends the process
	// Closed, the screens open hear it first (Template Updates' Not now).
	w.SetCloseIntercept(func() {
		sh.Close()
		w.Close()
	})
	ui.Focus = func() { w.Show(); w.RequestFocus() }
	w.SetContent(sh.Content())
	w.Resize(Size)
	w.CenterOnScreen()
	// Replaced by a later start: it quits, letting go of the window, without
	// the screens hearing it closed (Template Updates' Not now is mine to
	// choose). From a start before replacing: the screen asked for, in
	// front. Once the window has closed, the app's loop is gone: the window
	// is let go, so the next start holds it at once, and an ask that came in
	// meanwhile is dropped.
	var stopped atomic.Bool
	a.Lifecycle().SetOnStopped(func() {
		stopped.Store(true)
		holder.Release()
	})
	go func() {
		for line := range holder.Raised {
			fyne.Do(func() {
				if stopped.Load() {
					return
				}
				if line == instance.QuitAsk {
					a.Quit()
					return
				}
				w.Show()
				w.RequestFocus()
				if next, ok := asked(line); ok {
					sh.Show(next)
				}
			})
		}
	}()
	// Where updates stand shows in the sidebar.
	go func() {
		for ; !stopped.Load(); time.Sleep(StatusEvery) {
			if u, err := c.UpdateStatus(); err == nil {
				fyne.Do(func() { sh.UpdateStatus(&u) })
			}
		}
	}()
	// What the CLI answers waits for the app's loop, which starts here.
	sh.Show(which)
	w.ShowAndRun()
	return nil
}
