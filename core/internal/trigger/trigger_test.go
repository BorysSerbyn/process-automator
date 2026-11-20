package trigger

import (
	"testing"
	"time"
)

func TestNewAndInput(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 20, 0, 0, time.Local)
	o := New(Schedule, at)
	if o.ID != "schedule:2026-11-19T09:20:00" || !o.At.Equal(at) {
		t.Fatalf("got %+v", o)
	}
	in := o.Input()
	if in["kind"] != Schedule || in["id"] != o.ID || in["at"] != at.Format(time.RFC3339) {
		t.Fatalf("input %v", in)
	}
	if _, ok := in["data"]; ok {
		t.Fatal("no data, no data key")
	}
}
