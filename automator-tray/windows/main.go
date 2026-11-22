// automator-windows is the windows of an app built on process-automator on
// Linux, automator-ui's windows' counterpart: the tray starts it for one
// window (`automator-windows setup`, `automator-windows routine standup`,
// `automator-windows hook open-meet`),
// and it exits when that window closes.
// Like the tray, it acts only through the app's CLI, and its names come
// from the package's install.env. It's a program of its own so that the
// tray, always running, stays small and needs no cgo: Fyne (OpenGL, X11 and
// Wayland) is here, loaded only while a window shows.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/BorysSerbyn/process-automator/automator-tray/windows/internal/ui"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("automator-windows: ")
	dir := flag.String("app", "", "the app's share folder, holding install.env (default: found beside this program)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: automator-windows [--app <folder>] setup|settings|routines|routine <name> [review]|hooks|hook <name> [review]|watcher <name>|update|confirm-update")
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

// window is the window the arguments name.
func window(args []string) (tray.Window, error) {
	if len(args) == 0 {
		return tray.Window{}, errors.New("which window?")
	}
	w := tray.Window{Kind: tray.WindowKind(args[0])}
	switch w.Kind {
	case tray.SetupWindow, tray.SettingsWindow, tray.RoutinesWindow, tray.HooksWindow, tray.UpdateWindow, tray.ConfirmUpdateWindow:
		if len(args) == 1 {
			return w, nil
		}
	case tray.RoutineWindow, tray.HookWindow, tray.WatcherWindow:
		if len(args) == 3 && args[2] == "review" && w.Kind != tray.WatcherWindow {
			w.Review, args = true, args[:2]
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

// lockName is the window's lock: one window of each kind, one per routine,
// hook or watcher.
func lockName(w tray.Window) string {
	if !w.Named() {
		return "window-" + string(w.Kind)
	}
	sum := sha256.Sum256([]byte(w.Routine)) // a socket's path is short
	return "window-" + string(w.Kind) + "-" + hex.EncodeToString(sum[:8])
}

// reviewer is a window that can open its Turn On review.
type reviewer interface{ Review() }

// statusFollower is a window that shows run status as it changes.
type statusFollower interface{ Update(*cli.RunStatus) }

func run(dir string, which tray.Window) error {
	kind := which.Kind
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
	ask := "raise"
	if which.Review {
		ask = "review"
	}
	holder, err := instance.Ask(instance.Dir(os.Getenv, id), lockName(which), ask)
	if err != nil {
		return err
	}
	defer holder.Release()

	c := cli.CLI{Path: info.CLI, Command: info.Command}
	a := fyneapp.NewWithID(id + ".windows")
	var w fyne.Window
	var view content
	size := fyne.NewSize(640, 680)
	open := func(win tray.Window) {
		if err := tray.OpenWindow(info.CLI, win); err != nil {
			log.Print(err)
		}
	}
	switch kind {
	case tray.SetupWindow:
		w = a.NewWindow(info.Name + " Setup")
		view = ui.NewSetup(c, w, ui.Background, func(routine string) {
			open(tray.Window{Kind: tray.RoutineWindow, Routine: routine})
		})
	case tray.SettingsWindow:
		w = a.NewWindow(info.Name + " Settings")
		s := ui.NewSettings(c, w, ui.Background, info.Name)
		// While it shows, read usage again now and then.
		go func() {
			for range time.Tick(ui.UsageEvery) {
				fyne.Do(s.LoadUsage)
			}
		}()
		view = s
	case tray.RoutinesWindow:
		w = a.NewWindow("Routines")
		size = fyne.NewSize(600, 460)
		view = ui.NewRoutines(c, ui.Background,
			func(name string) { open(tray.Window{Kind: tray.RoutineWindow, Routine: name}) },
			func(name string) { open(tray.Window{Kind: tray.RoutineWindow, Routine: name, Review: true}) },
			func() { open(tray.Window{Kind: tray.SetupWindow}) },
			func() { open(tray.Window{Kind: tray.SettingsWindow}) })
	case tray.RoutineWindow:
		w = a.NewWindow(which.Routine)
		size = fyne.NewSize(680, 760)
		view = ui.NewRoutine(c, w, ui.Background, which.Routine, func() { w.Close() })
	case tray.HooksWindow:
		w = a.NewWindow("Hooks")
		size = fyne.NewSize(620, 520)
		view = ui.NewHooks(c, ui.Background,
			func(name string) { open(tray.Window{Kind: tray.HookWindow, Routine: name}) },
			func(name string) { open(tray.Window{Kind: tray.HookWindow, Routine: name, Review: true}) },
			func(name string) { open(tray.Window{Kind: tray.WatcherWindow, Routine: name}) })
	case tray.HookWindow:
		w = a.NewWindow(which.Routine)
		size = fyne.NewSize(680, 760)
		view = ui.NewHook(c, w, ui.Background, which.Routine, func() { w.Close() },
			func(name string) { open(tray.Window{Kind: tray.WatcherWindow, Routine: name}) })
	case tray.WatcherWindow:
		w = a.NewWindow(which.Routine)
		size = fyne.NewSize(640, 680)
		view = ui.NewWatcher(c, w, ui.Background, which.Routine, func() { w.Close() },
			func(name string) { open(tray.Window{Kind: tray.HookWindow, Routine: name}) })
	case tray.UpdateWindow, tray.ConfirmUpdateWindow:
		w = a.NewWindow("Updating")
		if kind == tray.ConfirmUpdateWindow {
			w.SetTitle(info.Name + " Update")
		}
		size = fyne.NewSize(460, 240)
		view = ui.NewUpdate(c, ui.Background, kind == tray.ConfirmUpdateWindow, func() { w.Close() })
	}
	// The Routines and Hooks windows, and a routine's, hook's or watcher's,
	// show run status as it changes.
	if f, ok := view.(statusFollower); ok {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var last *cli.RunStatus // the UI's thread's
		go c.Follow(ctx, func(s *cli.RunStatus) {
			fyne.Do(func() {
				last = s
				f.Update(s)
			})
		})
		// Shown again now and then, so one past its validUntil shows as not
		// responding.
		go func() {
			for range time.Tick(15 * time.Second) {
				fyne.Do(func() { f.Update(last) })
			}
		}()
	}
	w.SetMaster() // closing it ends the process
	w.SetContent(view.Content())
	w.Resize(size)
	w.CenterOnScreen()
	// Asked for again: to the front, and read again (automator-ui's show()).
	// Once the window has closed, the app's loop is gone: the window is let
	// go, so the next ask starts a process of its own, and one that came in
	// meanwhile is dropped.
	var stopped atomic.Bool
	a.Lifecycle().SetOnStopped(func() {
		stopped.Store(true)
		holder.Release()
	})
	go func() {
		for asked := range holder.Raised {
			fyne.Do(func() {
				if stopped.Load() {
					return
				}
				w.Show()
				w.RequestFocus()
				view.Load()
				if r, ok := view.(reviewer); ok && asked == "review" {
					r.Review()
				}
			})
		}
	}()
	// Opened to turn it on: its review shows once it's read, if it needs it.
	if r, ok := view.(reviewer); ok && which.Review {
		r.Review()
	}
	// What the CLI answers waits for the app's loop, which starts here.
	view.Load()
	w.ShowAndRun()
	return nil
}
