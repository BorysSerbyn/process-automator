//go:build unix

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const programHook = "description: Opens Meet\nexecutable: ./act\nwrites: false\non: {watcher: calendar, type: meeting.started}\nstaleness: 2m\nrate: 5\n"

const promptHook = "---\ndescription: Triage\nautomator.on: {watcher: inbox, type: email.received}\nautomator.batch: true\nautomator.rate: 10\n---\nLook at {{event.data.subject}}.\n"

func TestParse(t *testing.T) {
	h, err := Parse("open-meet", DescriptionFile, programHook)
	if err != nil {
		t.Fatal(err)
	}
	if h.Program == nil || h.Prompt != nil || h.On.Watcher != "calendar" || h.On.Type != "meeting.started" || !h.Program.Hook {
		t.Fatalf("%+v", h)
	}
	keys := map[string]any{}
	for _, s := range h.Settings() {
		keys[s.Key] = s.Default
	}
	if keys["hooks.open-meet.staleness"] != "2m" || keys["hooks.open-meet.rate"] != 5 || keys["hooks.open-meet.enabled"] != true {
		t.Fatalf("settings: %v", keys)
	}
	if _, ok := keys["hooks.open-meet.effort"]; ok {
		t.Fatal("a program hook has an LLM setting")
	}

	h, err = Parse("triage", "triage.prompt", promptHook)
	if err != nil {
		t.Fatal(err)
	}
	if h.Prompt == nil || !h.Prompt.Hook || !h.Batch || h.On.Watcher != "inbox" {
		t.Fatalf("%+v", h)
	}
	keys = map[string]any{}
	for _, s := range h.Settings() {
		keys[s.Key] = s.Default
	}
	if keys["hooks.triage.rate"] != 10 || keys["hooks.triage.effort"] != "default" {
		t.Fatalf("settings: %v", keys)
	}
	if _, ok := keys["hooks.triage.schedule"]; ok {
		t.Fatal("a hook has a schedule setting")
	}
	// Written dotted.
	if h, err := Parse("triage", "x.prompt", "---\nautomator.on.watcher: inbox\n---\nGo."); err != nil || h.On.Watcher != "inbox" || h.On.Type != "" {
		t.Fatalf("dotted on: %+v %v", h, err)
	}
	if !(On{Watcher: "w"}).Matches("anything") || (On{Watcher: "w", Type: "a"}).Matches("b") {
		t.Fatal("matching")
	}
}

func TestParseRefuses(t *testing.T) {
	for name, c := range map[string]struct{ path, src, want string }{
		"no on":              {DescriptionFile, "executable: ./act\nwrites: false\n", "on is required"},
		"no writes":          {DescriptionFile, "executable: ./act\non: {watcher: w}\n", "writes is required"},
		"a schedule":         {DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: w}\nschedule: \"0 9 * * *\"\n", "schedule"},
		"batch for programs": {DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: w}\nbatch: true\n", "batch is for an LLM hook"},
		"a bad type":         {DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: w, type: \"a b\"}\n", "on.type"},
		"a bad rate":         {DescriptionFile, "executable: ./act\nwrites: false\non: {watcher: w}\nrate: 0\n", "rate"},
		"prompt without on":  {"x.prompt", "---\ndescription: x\n---\nGo.", "automator.on is required"},
		"prompt schedule":    {"x.prompt", "---\nautomator.on: {watcher: w}\nautomator.schedule: \"0 9 * * *\"\n---\nGo.", "no automator.schedule"},
		"prompt parameters":  {"x.prompt", "---\nautomator.on: {watcher: w}\nautomator.parameters: [{name: x, type: string}]\n---\nGo.", "unknown field"},
		"prompt bad on":      {"x.prompt", "---\nautomator.on: {watcher: w, when: now}\n---\nGo.", "unknown field"},
		"prompt bad batch":   {"x.prompt", "---\nautomator.on: {watcher: w}\nautomator.batch: often\n---\nGo.", "batch"},
	} {
		if _, err := Parse("h", c.path, c.src); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", name, err, c.want)
		}
	}
}

func TestAddLoadDelete(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "act"), []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	desc := filepath.Join(src, DescriptionFile)
	prompt := filepath.Join(src, "triage.prompt")
	_ = os.WriteFile(desc, []byte(programHook), 0o600)
	_ = os.WriteFile(prompt, []byte(promptHook), 0o600)
	store := filepath.Join(t.TempDir(), "hooks")
	if _, err := Add(store, "open-meet", desc, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(store, "triage", prompt, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(store, "triage", prompt, false); err == nil {
		t.Fatal("added a hook twice")
	}
	set, err := Load(store)
	if err != nil || len(set.Hooks) != 2 || len(set.Errors) != 0 {
		t.Fatalf("%+v %v", set, err)
	}
	if p := set.Hooks[0].Program; p == nil || filepath.Dir(p.Executable) != filepath.Join(store, "open-meet") {
		t.Fatalf("the program hook's copy: %+v", set.Hooks[0])
	}
	if set.Hooks[1].Path != filepath.Join(store, "triage", PromptFile) {
		t.Fatalf("the prompt's copy: %s", set.Hooks[1].Path)
	}
	if path, err := Delete(store, "triage"); err != nil || !strings.HasSuffix(path, PromptFile) || Exists(store, "triage") {
		t.Fatalf("delete: %s %v", path, err)
	}
}
