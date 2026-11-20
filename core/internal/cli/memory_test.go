package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
)

// partialsBackend records the memories partial each run got, and returns a
// compacted list when asked to compact.
type partialsBackend struct{ memories *[]string }

func (partialsBackend) Name() string { return "fake" }
func (b partialsBackend) Run(ctx context.Context, j agent.Job) (*agent.Output, error) {
	if j.Name == "memory-compact" {
		return &agent.Output{
			Schema:     map[string]any{"type": "object"},
			Structured: map[string]any{"memories": []any{"Weeks start on Monday", "Use the staging API"}},
		}, nil
	}
	*b.memories = append(*b.memories, j.Partials["memories"])
	for _, tool := range j.Tools {
		if tool.Name == "report_outcome" {
			_, _ = tool.Call(ctx, map[string]any{"outcome": "done"})
		}
	}
	return &agent.Output{Text: "ok"}, nil
}

func TestMemories(t *testing.T) {
	a, set := enginetest.Fixture(t, map[string]string{"standup": "queue", "other": "queue"}, nil)
	var got []string
	orig := engine.Backends["claude-code"]
	engine.Backends["claude-code"] = engine.BackendDef{New: func(*engine.App) (agent.Backend, error) { return partialsBackend{&got}, nil }, AgentModel: orig.AgentModel,
		Ready: func(*engine.App) (string, string) { return "", "" }}
	cfg := a.Layout.Config()
	for _, args := range [][]string{
		{"memory", "add", "Weeks start on Monday"},
		{"memory", "add", "The team's thread is in #eng-updates", "--routine", "standup"},
		{"memory", "add", "not for standup", "--routine", "other"},
	} {
		if _, err := cli(t, cfg, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if _, err := cli(t, cfg, "memory", "add", "x", "--routine", "nope"); err == nil {
		t.Fatal("memories for a routine that doesn't exist")
	}
	r, _ := set.Find("standup")
	if _, err := a.Execute(context.Background(), set, r, engine.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "Monday") || !strings.Contains(got[0], "#eng-updates") || strings.Contains(got[0], "not for standup") {
		t.Fatalf("standup's prompt got: %q", got)
	}
	// The cap.
	_, _ = cli(t, cfg, "settings", "set", "memories.max-chars", "200")
	if _, err := cli(t, cfg, "memory", "add", strings.Repeat("long ", 40)); err == nil || !strings.Contains(err.Error(), "compact") {
		t.Fatalf("past the cap: %v", err)
	}
	// Compacting shows the result and saves only when confirmed.
	_, _ = cli(t, cfg, "settings", "set", "memories.max-chars", "4000")
	_, _ = cli(t, cfg, "memory", "add", "Weeks begin on Mondays")
	_, _ = cli(t, cfg, "memory", "add", "Use the staging API")
	if _, err := cli(t, cfg, "memory", "compact"); err == nil {
		t.Fatal("compacted without asking")
	}
	out, err := cli(t, cfg, "memory", "compact", "--yes")
	if err != nil || !strings.Contains(out, "- Weeks begin on Mondays") || !strings.Contains(out, "Saved: 2 memories, from 3") {
		t.Fatalf("compact: %s %v", out, err)
	}
	// Deleting a routine forgets its memories.
	_, _ = cli(t, cfg, "routines", "delete", "other")
	if out, _ := cli(t, cfg, "memory", "list"); strings.Contains(out, "not for standup") {
		t.Fatalf("deleted routine's memories kept:\n%s", out)
	}
}
