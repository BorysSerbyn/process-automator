package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRegistryIsWellFormed(t *testing.T) {
	keyRE := regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*$`)
	seen := map[string]bool{}
	for _, s := range Registry {
		if !keyRE.MatchString(s.Key) {
			t.Errorf("bad key %q", s.Key)
		}
		if seen[s.Key] {
			t.Errorf("duplicate key %q", s.Key)
		}
		seen[s.Key] = true
		if s.Description == "" {
			t.Errorf("%s: missing description", s.Key)
		}
		if _, err := s.Normalize(s.Default); err != nil {
			t.Errorf("%s: default is invalid: %v", s.Key, err)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		key, in string
		want    any
		wantErr bool
	}{
		{"llm.usage-limit", "50%", 50.0, false},
		{"llm.usage-limit", "75", 75.0, false},
		{"llm.usage-limit", "150%", nil, true},
		{"history.retention-days", "30", 30, false},
		{"history.retention-days", "0", nil, true},
		{"llm.budget.window", "20m", "20m", false},
		{"llm.budget.window", "soon", nil, true},
	}
	for _, c := range cases {
		def, ok := Lookup(c.key)
		if !ok {
			t.Fatalf("no setting %s", c.key)
		}
		got, err := def.Parse(c.in)
		if (err != nil) != c.wantErr || (!c.wantErr && got != c.want) {
			t.Errorf("Parse(%s, %q) = %v, %v; want %v (err=%v)", c.key, c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestStorePreservesRestOfConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# my servers\nmcpServers:\n  extra:\n    command: /bin/extra # keep me\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Store{Path: path, Extra: RoutineSettings("standup", map[string]any{"schedule": "15 9 * * 1-5"})}

	if _, err := s.Set("llm.usage-limit", "40%"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("routines.standup.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	st, err := s.Get("llm.usage-limit")
	if err != nil || st.Value != 40.0 || st.IsDefault {
		t.Fatalf("Get = %+v, %v", st, err)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{"# my servers", "# keep me", "command: /bin/extra", "llm.usage-limit: 40"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("config lost %q:\n%s", want, raw)
		}
	}

	if _, err := s.Reset("llm.usage-limit"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Get("llm.usage-limit")
	if !st.IsDefault || st.Value != 50.0 {
		t.Fatalf("after reset: %+v", st)
	}
	standup, _ := s.Get("routines.standup.enabled")
	if standup.Value != false {
		t.Fatalf("unrelated setting changed: %+v", standup)
	}
}

func TestStoreMissingFileAndUnknownKey(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "sub", "config.yaml")}
	all, err := s.List()
	if err != nil || len(all) != len(Registry) || !all[0].IsDefault {
		t.Fatalf("List on missing file: %v %v", all, err)
	}
	if _, err := s.Set("nope", "1"); err == nil {
		t.Fatal("want unknown key error")
	}
	if _, err := s.Set("history.retention-days", "7"); err != nil {
		t.Fatalf("Set should create the file and dir: %v", err)
	}
}

func TestRoutineSettings(t *testing.T) {
	keys := func(defs []Setting) map[string]any {
		m := map[string]any{}
		for _, d := range defs {
			m[d.Key] = d.Default
		}
		return m
	}
	manual := keys(RoutineSettings("adhoc", nil))
	if _, ok := manual["routines.adhoc.schedule"]; ok || manual["routines.adhoc.enabled"] != true ||
		manual["routines.adhoc.timeout"] != "20m" || manual["routines.adhoc.effort"] != "default" {
		t.Fatalf("manual-only routines get no schedule fields but the rest: %v", manual)
	}
	s := RoutineSettings("standup", map[string]any{"schedule": "15 9 * * 1-5", "timeout": "5m"})
	got := keys(s)
	if got["routines.standup.schedule"] != "15 9 * * 1-5" || got["routines.standup.staleness"] != "1h" || got["routines.standup.timeout"] != "5m" {
		t.Fatalf("scheduled, defaults from the file: %v", got)
	}
	for _, def := range s {
		if def.Default == nil {
			continue // no default: unset until a file or setting gives one
		}
		if _, err := def.Normalize(def.Default); err != nil {
			t.Errorf("%s default invalid: %v", def.Key, err)
		}
	}
	st := Store{Path: filepath.Join(t.TempDir(), "c.yaml"), Extra: s}
	if _, err := st.Set("routines.standup.schedule", "every morning"); err == nil {
		t.Fatal("bad cron accepted")
	}
	if _, err := st.Set("routines.standup.schedule", "0 9 * * 1-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Set("routines.standup.effort", "extreme"); err == nil {
		t.Fatal("bad effort accepted")
	}
	if _, err := st.Set("routines.standup.timeout", "90m"); err != nil {
		t.Fatal(err)
	}
	if _, err := (Store{Path: st.Path}).Set("routines.standup.enabled", "true"); err == nil {
		t.Fatal("routine setting must be unknown without its routine")
	}
}

func TestListAndPathTypes(t *testing.T) {
	list := Setting{Key: "x.tools", Type: TypeList, Enum: []string{"Bash", "Read", "Edit"}}
	if v, err := list.Parse(" Bash, Read ,"); err != nil || fmt.Sprint(v) != "[Bash Read]" {
		t.Fatalf("parse: %v %v", v, err)
	}
	if _, err := list.Parse("Bash,Rm"); err == nil {
		t.Fatal("item outside Enum accepted")
	}
	if v, err := list.Normalize([]any{"Edit"}); err != nil || fmt.Sprint(v) != "[Edit]" {
		t.Fatalf("normalize from YAML: %v %v", v, err)
	}
	if _, err := list.Normalize([]any{1}); err == nil {
		t.Fatal("non-text item accepted")
	}
	if v, _ := list.Parse(""); fmt.Sprint(v) != "[]" {
		t.Fatalf("empty list: %v", v)
	}

	// Round trip through the config file: written as a YAML list, read back as one.
	st := Store{Path: filepath.Join(t.TempDir(), "c.yaml"), Extra: []Setting{list, {Key: "x.folder", Type: TypePath}}}
	if _, err := st.Set("x.tools", "Bash,Edit"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Get("x.tools"); err != nil || fmt.Sprint(got.Value) != "[Bash Edit]" {
		t.Fatalf("round trip: %v %v", got.Value, err)
	}
	if _, err := st.Set("x.folder", ""); err == nil {
		t.Fatal("empty path accepted")
	}
	home, _ := os.UserHomeDir()
	if p, _ := ExpandPath("~/code/x"); p != filepath.Join(home, "code/x") {
		t.Fatalf("expand: %s", p)
	}
	if p, _ := ExpandPath("/abs"); p != "/abs" {
		t.Fatalf("absolute paths stay: %s", p)
	}
}

// A routine file's values become canonical defaults (a YAML list is []any).
func TestRoutineSettingsNormalizeFileValues(t *testing.T) {
	for _, s := range RoutineSettings("r", map[string]any{"builtinTools": []any{"Read", "Grep"}, "timeout": "5m"}) {
		if s.Key == "routines.r.builtinTools" {
			if got, ok := s.Default.([]string); !ok || len(got) != 2 || got[0] != "Read" {
				t.Fatalf("builtinTools default %#v", s.Default)
			}
		}
	}
}

func TestClickActionChecked(t *testing.T) {
	st := Store{Path: filepath.Join(t.TempDir(), "c.yaml")}
	for in, ok := range map[string]bool{"show-run": true, "open ~/notes.md": true, "run say done": true, "none": true, "open": false, "launch x": false, "show-run now": false} {
		if _, err := st.Set("notifications.on-click", in); (err == nil) != ok {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("settings:\n  history.retention-days: 30\n  llm.budget.window: 2h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	extra := RoutineSettings("r", map[string]any{"builtinTools": []any{"Read"}})
	v, err := (Store{Path: path, Extra: extra}).Values()
	if err != nil {
		t.Fatal(err)
	}
	if v.Int("history.retention-days") != 30 || v.String("llm.backend") != "api" || !v.Bool("notifications.failures") ||
		v.Float("llm.budget.weekly-usd") != 20 || !slices.Equal(v.List("routines.r.builtinTools"), []string{"Read"}) {
		t.Fatalf("values: %+v", v)
	}
	if d, err := v.Duration("llm.budget.window"); err != nil || d != 2*time.Hour {
		t.Fatalf("duration: %v %v", d, err)
	}
	if _, err := v.Get("no.such"); err == nil || v.Value("no.such") != nil {
		t.Fatal("an undeclared key has a value")
	}
}
