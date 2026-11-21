package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/firebase/genkit/go/genkit"
)

var fruitSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"fruit": map[string]any{"type": "string"}, "letters": map[string]any{"type": "integer"}},
	"required":   []any{"fruit", "letters"},
}

func TestCheckOutput(t *testing.T) {
	if err := checkOutput(fruitSchema, map[string]any{"fruit": "fig", "letters": 3}); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := checkOutput(fruitSchema, map[string]any{"fruit": "fig"}); err == nil || !strings.Contains(err.Error(), "output schema") {
		t.Fatalf("missing field: %v", err)
	}
	if err := checkOutput(fruitSchema, nil); err == nil {
		t.Fatal("no result accepted")
	}
}

// A routine's output: schema (picoschema) renders to the JSON schema backends get.
func TestOutputSchemaFromPrompt(t *testing.T) {
	g := genkit.Init(context.Background())
	p, err := genkit.LoadPromptFromSource(g, "---\noutput:\n  schema:\n    fruit: string\n    letters: integer\n---\nName a fruit.", "f", "")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := p.Render(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := outputSchema(opts)
	if s == nil || s["type"] != "object" {
		t.Fatalf("schema: %#v", s)
	}
	if err := checkOutput(s, map[string]any{"fruit": "fig", "letters": 3}); err != nil {
		t.Fatalf("a matching result failed the rendered schema: %v", err)
	}
}

func TestClaudeCodeStructuredOutput(t *testing.T) {
	cc := &ClaudeCode{Path: "claude"}
	cmd, err := cc.Command(context.Background(), CLIRun{System: "s", MCP: MCPEndpoint{Name: "procauto", URL: "u", Token: "t"}, OutputSchema: fruitSchema})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := argValue(cmd.Args, "--json-schema"); !ok || !strings.Contains(v, `"letters"`) {
		t.Fatalf("--json-schema missing: %v", cmd.Args)
	}
	out, err := cc.Result(CLIRun{}, []byte(`{"result":"{}","structured_output":{"fruit":"fig","letters":3},"usage":{}}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := out.Structured.(map[string]any); m["fruit"] != "fig" {
		t.Fatalf("structured: %#v", out.Structured)
	}
}
