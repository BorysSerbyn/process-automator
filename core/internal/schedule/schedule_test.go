package schedule

import (
	"slices"
	"testing"
	"time"
)

func TestText(t *testing.T) {
	if got := Text("20 9 * * 1-5"); got != "At 09:20, Monday through Friday" {
		t.Errorf("weekdays: %q", got)
	}
	if got := Text("not cron"); got != "not cron" {
		t.Errorf("undescribable: %q", got)
	}
}

func TestPickerRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		p    Picker
		cron string
	}{
		{Picker{[]string{"mon", "tue", "wed", "thu", "fri"}, "09:20"}, "20 9 * * 1-5"},
		{Picker{slices.Clone(Days), "07:05"}, "5 7 * * *"},
		{Picker{[]string{"mon", "wed", "fri"}, "18:00"}, "0 18 * * 1,3,5"},
		{Picker{[]string{"sun", "sat"}, "10:00"}, "0 10 * * 0,6"},
	} {
		got, err := Build(tc.p)
		if err != nil || got != tc.cron {
			t.Errorf("Build(%v) = %q, %v; want %q", tc.p, got, err, tc.cron)
			continue
		}
		back := FromCron(got)
		if back == nil || !slices.Equal(back.Days, tc.p.Days) || back.At != tc.p.At {
			t.Errorf("FromCron(%q) = %+v, want %+v", got, back, tc.p)
		}
	}
	for _, expr := range []string{"*/15 * * * *", "0 9 1 * *", "0 9-17 * * 1-5", "0 9 * * MON"} {
		if p := FromCron(expr); p != nil {
			t.Errorf("FromCron(%q) = %+v, want nil (not a picker schedule)", expr, p)
		}
	}
	if p := FromCron("0 9 * * 7"); p == nil || !slices.Equal(p.Days, []string{"sun"}) {
		t.Errorf("7 is Sunday: %+v", p)
	}
	if _, err := Build(Picker{Days: []string{"someday"}, At: "09:00"}); err == nil {
		t.Error("an unknown day was accepted")
	}
	if _, err := Build(Picker{Days: []string{"mon"}, At: "9am"}); err == nil {
		t.Error("a bad time was accepted")
	}
}

func TestDescribe(t *testing.T) {
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.Local) // a Friday
	info, err := Describe("20 9 * * 1-5", now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Next) != 2 || info.Next[0] != time.Date(2026, 11, 20, 9, 20, 0, 0, time.Local) ||
		info.Next[1] != time.Date(2026, 11, 23, 9, 20, 0, 0, time.Local) {
		t.Errorf("next: %v", info.Next)
	}
	if info.Picker == nil || info.Picker.At != "09:20" {
		t.Errorf("picker: %+v", info.Picker)
	}
	if _, err := Describe("nope", now, 1); err == nil {
		t.Error("an invalid schedule was described")
	}
}
