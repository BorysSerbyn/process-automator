package desktop

import (
	"bytes"
	"context"
	"image/png"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

// fakeObject is the notifications service's object: it records calls and
// answers them.
type fakeObject struct {
	calls []fakeCall
	caps  []string
	// down: the service isn't there (yet)
	down bool
}

type fakeCall struct {
	method string
	args   []any
}

func (o *fakeObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	o.calls = append(o.calls, fakeCall{method, args})
	if _, ok := ctx.Deadline(); !ok {
		return &dbus.Call{Err: dbus.ErrMsgInvalidArg} // every call is bounded
	}
	if o.down {
		return &dbus.Call{Err: dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}}
	}
	switch method {
	case notificationsIface + ".Notify":
		return &dbus.Call{Body: []any{uint32(len(o.calls))}}
	case notificationsIface + ".GetCapabilities":
		return &dbus.Call{Body: []any{o.caps}}
	}
	return &dbus.Call{Err: dbus.ErrMsgUnknownMethod}
}

// A note goes out as the app: its name and icon, the note's summary, body
// and actions, normal urgency; persistent ones never expire.
func TestNotifyPostsAsTheApp(t *testing.T) {
	o := &fakeObject{}
	n := &Notifications{obj: o, appName: "Acme Automator", appIcon: "file:///opt/acme/share/acme/tray/app.png"}
	id, err := n.Notify(tray.Note{Summary: "standup failed", Body: "why", Actions: []string{"default", "Open"}})
	if err != nil || id != 1 {
		t.Fatal(id, err)
	}
	urgency := map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))}
	want := []any{"Acme Automator", uint32(0), "file:///opt/acme/share/acme/tray/app.png", "standup failed", "why",
		[]string{"default", "Open"}, urgency, int32(-1)}
	if o.calls[0].method != "org.freedesktop.Notifications.Notify" || !reflect.DeepEqual(o.calls[0].args, want) {
		t.Fatalf("%s %#v", o.calls[0].method, o.calls[0].args)
	}
	_, _ = n.Notify(tray.Note{Summary: "Update to v2?", Persistent: true})
	if args := o.calls[1].args; !reflect.DeepEqual(args[5], []string{}) || args[7] != int32(0) {
		t.Fatalf("%#v", args)
	}
}

func TestCanAskIsTheActionsCapability(t *testing.T) {
	for caps, want := range map[string]bool{"body,actions,persistence": true, "body": false} {
		o := &fakeObject{caps: splitComma(caps)}
		n := &Notifications{obj: o}
		first, second := n.CanAsk(), n.CanAsk()
		if first != want || second != want || len(o.calls) != 1 {
			t.Errorf("%s: %v %v (asked %d times)", caps, first, second, len(o.calls))
		}
	}
}

// A server that didn't answer (not up yet) is asked again, not taken to have
// no actions for the tray's whole life.
func TestCanAskAsksAgainUntilTheServerAnswers(t *testing.T) {
	o := &fakeObject{caps: []string{"actions"}, down: true}
	n := &Notifications{obj: o}
	if n.CanAsk() {
		t.Fatal("no server, yet it can ask")
	}
	o.down = false
	first, second := n.CanAsk(), n.CanAsk() // the second from what it remembers
	if !first || !second || len(o.calls) != 2 {
		t.Fatalf("asked %d times", len(o.calls))
	}
}

func splitComma(s string) []string {
	var out []string
	for _, b := range bytes.Split([]byte(s), []byte(",")) {
		out = append(out, string(b))
	}
	return out
}

// The service's signals reach the tray; losing the bus name, or the bus,
// quits it.
func TestDispatch(t *testing.T) {
	ch := make(chan *dbus.Signal, 8)
	var got []string
	ev := Events{
		Invoked: func(id uint32, action string) { got = append(got, "invoked "+string(rune('0'+id))+" "+action) },
		Closed:  func(id uint32) { got = append(got, "closed "+string(rune('0'+id))) },
		Lost:    func() { got = append(got, "lost") },
		Gone:    func() { got = append(got, "gone") },
	}
	ch <- &dbus.Signal{Name: "org.freedesktop.Notifications.ActionInvoked", Body: []any{uint32(3), "default"}}
	ch <- &dbus.Signal{Name: "org.freedesktop.Notifications.ActionInvoked", Body: []any{"bad"}}
	ch <- &dbus.Signal{Name: "org.freedesktop.Notifications.NotificationClosed", Body: []any{uint32(3), uint32(2)}}
	ch <- &dbus.Signal{Name: "org.kde.StatusNotifierWatcher.Whatever", Body: []any{}}
	ch <- &dbus.Signal{Name: "org.freedesktop.DBus.NameLost", Body: []any{"com.acme.acme-auto.Tray"}}
	close(ch)
	Dispatch(ch, ev)
	if want := []string{"invoked 3 default", "closed 3", "lost", "gone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestBusName(t *testing.T) {
	for id, want := range map[string]string{
		"dev.procauto":       "dev.procauto.Tray",
		"com.acme.acme-auto": "com.acme.acme-auto.Tray",
		"ci.test.x":          "ci.test.x.Tray",
		"com.1acme.x":        "",
		"":                   "",
	} {
		if got := BusName(id); got != want {
			t.Errorf("%q: %q, want %q", id, got, want)
		}
	}
}

// Underscores in a row's text show as underscores, not mnemonics.
func TestLabel(t *testing.T) {
	if got := Label("weekly_report — Next: _9"); got != "weekly__report — Next: __9" {
		t.Fatal(got)
	}
}

func TestDotsArePNGs(t *testing.T) {
	for _, h := range []string{"ok", "error", "none", "unknown"} {
		img, err := png.Decode(bytes.NewReader(Dot(h)))
		if err != nil || img.Bounds().Dx() != 16 {
			t.Fatalf("%s: %v", h, err)
		}
		if _, _, _, a := img.At(8, 8).RGBA(); a != 0xffff {
			t.Errorf("%s: the dot's middle isn't solid", h)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("%s: the corner isn't clear", h)
		}
	}
}
