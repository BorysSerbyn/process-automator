// automator-tray is the Linux tray of an app built on process-automator:
// the counterpart of automator-ui's menu bar indicator. It shows the core's
// run status in its icon and menu, delivers the app's notifications and
// offers updates, through the app's CLI only. Windows open through the
// window seam (tray.OpenWindow).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"fyne.io/systray"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/app"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/desktop"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

// AppIcon is the app's icon in the tray's icons folder (notifications).
const AppIcon = "app.png"

func main() {
	log.SetFlags(0)
	log.SetPrefix("automator-tray: ")
	dir := flag.String("app", "", "the app's share folder, holding install.env and the tray's icons (default: found beside the tray)")
	flag.Parse()
	if err := run(*dir); err != nil {
		log.Fatal(err)
	}
}

func run(dir string) error {
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
	icons := map[tray.Frame][]byte{}
	for _, f := range tray.Frames() {
		if icons[f], err = os.ReadFile(filepath.Join(info.Icons, f.File())); err != nil {
			return fmt.Errorf("the tray's icons: %w", err)
		}
	}
	session, err := desktop.Connect()
	if err != nil {
		return err
	}
	icon := url.URL{Scheme: "file", Path: filepath.Join(info.Icons, AppIcon)}
	notifications := desktop.NewNotifications(session.Conn, info.Name, icon.String())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer cancel()
	v := &view{icons: icons, dots: map[string][]byte{}}
	for h := range desktop.Colours {
		v.dots[h] = desktop.Dot(h)
	}
	t := tray.New(info, v, notifications, tray.OpenWindow, systray.Quit)
	v.choose = t.Choose
	if err := session.Listen(desktop.Events{Invoked: t.Desk().Invoked, Closed: t.Desk().Closed, Lost: systray.Quit, Gone: systray.Quit}); err != nil {
		return err
	}
	// Claimed once listening, so losing the name to a tray started right
	// after this one isn't missed.
	if err := session.Claim(desktop.BusName(info.BundleID)); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	// Set before Run: the title is the item's Id, which the desktop keeps
	// its place and settings under.
	systray.SetTitle(info.Name)
	systray.SetTooltip(info.Name)
	systray.SetIcon(icons[tray.Frame{Light: tray.Red}])
	systray.Run(func() {
		go t.Run(ctx)
		go func() {
			for range systray.TrayOpenedCh {
				t.MenuOpened()
			}
		}()
	}, cancel)
	return nil
}

// view is the tray icon and its menu, through fyne.io/systray
// (StatusNotifierItem and dbusmenu, over D-Bus).
type view struct {
	icons  map[tray.Frame][]byte
	dots   map[string][]byte
	choose func(tray.Action)
}

func (v *view) SetIcon(f tray.Frame) { systray.SetIcon(v.icons[f]) }

// SetMenu replaces the menu (the tray sets it only when it changed).
func (v *view) SetMenu(items []tray.MenuItem) {
	systray.ResetMenu()
	for _, it := range items {
		v.add(nil, it)
	}
}

func (v *view) add(parent *systray.MenuItem, it tray.MenuItem) {
	if it.Separator {
		if parent == nil {
			systray.AddSeparator()
		} else {
			parent.AddSeparator()
		}
		return
	}
	var m *systray.MenuItem
	if parent == nil {
		m = systray.AddMenuItem(desktop.Label(it.Text), "")
	} else {
		m = parent.AddSubMenuItem(desktop.Label(it.Text), "")
	}
	if it.Health != "" {
		m.SetIcon(v.dots[it.Health])
	}
	for _, sub := range it.Submenu {
		v.add(m, sub)
	}
	if it.Action.Kind == tray.None {
		if len(it.Submenu) == 0 {
			m.Disable()
		}
		return
	}
	go func() {
		for range m.ClickedCh {
			v.choose(it.Action)
		}
	}()
}
