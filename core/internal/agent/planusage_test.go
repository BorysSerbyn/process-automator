package agent

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseUsageReport(t *testing.T) {
	stream := []byte(`{"type":"system","subtype":"init"}
{"type":"assistant","message":{"model":"<synthetic>"},"usage_report":{"session":{"total_cost_usd":0},"rate_limits":{"limits":[` +
		`{"kind":"session","group":"session","percent":2,"resets_at":"2026-11-20T23:49:59.978241+00:00","scope":null},` +
		`{"kind":"weekly_all","group":"weekly","percent":47,"resets_at":"2026-11-24T03:59:59.978266+00:00","scope":null},` +
		`{"kind":"weekly_scoped","group":"weekly","percent":10,"resets_at":"2026-11-24T03:59:59Z","scope":{"model":{"display_name":"Fable"}}},` +
		`{"kind":"something_new","percent":null}]}}}
{"type":"result","subtype":"success"}
`)
	got, err := parseUsageReport(stream)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Kind != "session" || got[0].Percent != 2 || !got[0].ResetsAt.Equal(time.Date(2026, 11, 20, 23, 49, 59, 978241000, time.UTC)) {
		t.Errorf("session: %+v", got[0])
	}
	if got[2].Kind != "weekly_scoped" || got[2].Model != "Fable" {
		t.Errorf("scoped: %+v", got[2])
	}
	if _, err := parseUsageReport([]byte(`{"type":"result","result":"Usage: ..."}`)); err == nil {
		t.Error("no report read as one")
	}
	// Renamed fields: a report with nothing readable is a failed read, not "nothing known".
	renamed := []byte(`{"type":"assistant","usage_report":{"rate_limits":{"limits":[{"kind":"session","pct":5,"resets_at":"2026-11-20T23:49:59Z"}]}}}`)
	if _, err := parseUsageReport(renamed); err == nil || !strings.Contains(err.Error(), "format may have changed") {
		t.Errorf("renamed fields: %v", err)
	}
}

// The real /usage output, when the research run saved it.
func TestParseUsageReportSample(t *testing.T) {
	b, err := os.ReadFile("testdata/usage-stream.jsonl")
	if err != nil {
		t.Skip("no sample")
	}
	if got, err := parseUsageReport(b); err != nil || len(got) == 0 {
		t.Errorf("%+v %v", got, err)
	}
}
