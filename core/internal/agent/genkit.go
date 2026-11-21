package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Genkit runs the model loop in-process with Genkit, against the Anthropic API
// (pay-per-token, needs an API key). The frontmatter's model and config apply.
type Genkit struct {
	APIKey string
}

// Name implements Backend.
func (*Genkit) Name() string { return "api" }

// Run implements Backend.
func (b *Genkit) Run(ctx context.Context, j Job) (*Output, error) {
	if j.Effort != "" {
		return nil, fmt.Errorf("effort %q isn't supported by the api backend yet; leave it at default or use an agent CLI backend (claude-code or codex)", j.Effort)
	}
	if j.Folder != "" || j.SaveSession || j.SessionID != "" || len(j.BuiltinTools) > 0 {
		return nil, errors.New("folder, session and builtinTools need an agent CLI backend (llm.backend claude-code or codex)")
	}
	g := newGenkit(ctx, j, genkit.WithPlugins(&anthropic.Anthropic{APIKey: b.APIKey}))
	for _, t := range j.Tools {
		call := t.Call
		genkit.DefineTool(g, t.Name, t.Description,
			func(tc *ai.ToolContext, input any) (string, error) {
				args, err := toMap(input)
				if err != nil {
					return "", err
				}
				out, err := call(tc, args)
				if err != nil {
					return "", err
				}
				if out.IsError {
					return "Tool error: " + pluginapi.ResultText(out), nil
				}
				return pluginapi.ResultText(out), nil
			},
			ai.WithInputSchema(t.InputSchema),
		)
	}
	p, err := genkit.LoadPromptFromSource(g, j.Source, j.Name, "")
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", j.Name, err)
	}
	req, err := p.Render(ctx, j.Input)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: render: %w", j.Name, err)
	}
	if j.Instructions != "" {
		req.Messages = withSystemText(req.Messages, j.Instructions)
	}
	req.MaxTurns = j.MaxTurns
	req.Tools = nil
	for _, t := range j.Tools {
		req.Tools = append(req.Tools, t.Name)
	}
	resp, err := genkit.GenerateWithRequest(ctx, g, req, nil, nil)
	if err != nil {
		return nil, err
	}
	out := &Output{Text: resp.Text()}
	if schema := outputSchema(req); schema != nil {
		out.Schema = schema
		var v any
		if err := resp.Output(&v); err == nil {
			out.Structured = v
		}
	}
	u := resp.TotalUsage
	if u == nil {
		u = resp.Usage
	}
	if u != nil {
		out.InputTokens, out.OutputTokens, out.CachedTokens = u.InputTokens, u.OutputTokens, u.CachedContentTokens
		out.CostUSD, out.Priced = apiCost(req.Model, u.InputTokens, u.OutputTokens, u.CachedContentTokens, u.CacheWriteTokens)
		out.Model = req.Model
	}
	return out, nil
}

// withSystemText adds text to the prompt's system message, creating one if needed.
func withSystemText(msgs []*ai.Message, text string) []*ai.Message {
	for _, m := range msgs {
		if m.Role == ai.RoleSystem {
			m.Content = append(m.Content, ai.NewTextPart("\n\n"+text))
			return msgs
		}
	}
	return append([]*ai.Message{ai.NewSystemTextMessage(text)}, msgs...)
}
