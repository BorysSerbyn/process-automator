package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// Every command with --json declares its output type (jsonFlag), so it lands
// in the contract.
func TestEveryJSONCommandDeclaresItsOutput(t *testing.T) {
	root := NewRoot(nil)
	outputs := JSONOutputs(root)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() && c.Flag("json") != nil {
			if path := strings.TrimPrefix(c.CommandPath(), "procauto "); outputs[path] == nil {
				t.Errorf("%q has a --json flag that doesn't declare its output (use jsonFlag)", path)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	if len(outputs) == 0 {
		t.Fatal("no command declares a JSON output")
	}
}

func TestNotImplementedSettingsAreLabelled(t *testing.T) {
	// A setting declared ahead of its feature (none are, right now).
	orig := settings.Registry
	settings.Registry = append(slices.Clone(orig), settings.Setting{
		Key: "pt.ahead", Type: settings.TypeBool, Default: false, NotImplemented: true, Description: "test only",
	})
	t.Cleanup(func() { settings.Registry = orig })
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	run := func(args ...string) (string, string) {
		root := NewRoot(nil)
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(append(args, "--config", cfg))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String(), errOut.String()
	}
	list, _ := run("settings", "list")
	for _, line := range strings.Split(list, "\n") {
		if strings.HasPrefix(line, "pt.ahead") && !strings.Contains(line, notImplemented) {
			t.Errorf("not labelled: %q", line)
		}
		if strings.HasPrefix(line, "llm.backend") && strings.Contains(line, notImplemented) {
			t.Errorf("wrongly labelled: %q", line)
		}
	}
	if _, stderr := run("settings", "set", "pt.ahead", "true"); !strings.Contains(stderr, "no effect") {
		t.Errorf("set gave no warning: %q", stderr)
	}
}

func TestJSONErrors(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	run := func(args ...string) (string, error) {
		root := NewRoot(nil)
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(append([]string{"--config", cfg}, args...))
		err := execute(root)
		return out.String(), err
	}
	out, err := run("settings", "list", "--scope", "bogus", "--json")
	var e ErrorOutput
	if err == nil || json.Unmarshal([]byte(out), &e) != nil || !strings.Contains(e.Error, "unknown --scope") {
		t.Errorf("--json failure printed %q (err %v)", out, err)
	}
	if out, _ := run("settings", "list", "--scope", "bogus"); out != "" {
		t.Errorf("without --json, the error is only on stderr; stdout got %q", out)
	}
	// A failure the output already reports prints no second object.
	var asJSON bool
	check := &cobra.Command{Use: "check", SilenceErrors: true, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		_ = writeJSON(cmd.OutOrStdout(), []string{"x"})
		return reported(errors.New("1 problem"))
	}}
	jsonFlag(check.Flags(), &asJSON, []string{})
	check.SetArgs([]string{"--json"})
	var checkOut bytes.Buffer
	check.SetOut(&checkOut)
	var rows []string
	if err := execute(check); err == nil || json.Unmarshal(checkOut.Bytes(), &rows) != nil {
		t.Errorf("a reported failure printed %q (err %v)", checkOut.String(), err)
	}
}

func TestLoginItemsSummary(t *testing.T) {
	none := summarizeLoginItems([]LoginItem{{Name: "ui"}, {Name: "scheduler"}})
	if none.Installed || none.Enabled {
		t.Errorf("nothing installed reads as %+v", none)
	}
	on := summarizeLoginItems([]LoginItem{{Name: "ui", Installed: true, Enabled: true}, {Name: "scheduler"}})
	if !on.Installed || !on.Enabled {
		t.Errorf("the installed item starting at login reads as %+v", on)
	}
	off := summarizeLoginItems([]LoginItem{{Name: "ui", Installed: true, Enabled: true}, {Name: "scheduler", Installed: true}})
	if off.Enabled {
		t.Errorf("one installed item not starting at login reads as %+v", off)
	}
}
