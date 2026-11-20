package runstatus

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The shared example must round-trip unchanged; automator-ui decodes the same file.
func TestContractExampleRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/examples/run-status.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Status
	if err := dec.Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != SchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", s.SchemaVersion, SchemaVersion)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(raw, &a)
	_ = json.Unmarshal(out, &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("round-trip mismatch:\n in: %s\nout: %s", ja, jb)
	}
}

// The schema enums (struct tags) must list exactly the Go constants.
func TestSchemaEnumsMatchConstants(t *testing.T) {
	enum := func(field reflect.StructField) []string {
		var out []string
		for _, part := range strings.Split(field.Tag.Get("jsonschema"), ",") {
			if v, ok := strings.CutPrefix(part, "enum="); ok {
				out = append(out, v)
			}
		}
		return out
	}
	h, _ := reflect.TypeOf(Status{}).FieldByName("Health")
	if got := strings.Join(enum(h), ","); got != strings.Join([]string{string(HealthOK), string(HealthError), string(HealthNone)}, ",") {
		t.Errorf("health enum = %s", got)
	}
	o, _ := reflect.TypeOf(Run{}).FieldByName("Outcome")
	want := []string{string(OutcomeSuccess), string(OutcomeFailed), string(OutcomeSkipped), string(OutcomeMissed), string(OutcomeNeedsReauth), string(OutcomeRunning), string(OutcomeQueued), string(OutcomeStopped)}
	if got := strings.Join(enum(o), ","); got != strings.Join(want, ",") {
		t.Errorf("outcome enum = %s", got)
	}
}
