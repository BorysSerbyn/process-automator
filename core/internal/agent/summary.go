package agent

import "fmt"

// Summary is the run in one line: backend, tool calls, tokens, cost and prompt.
func (res *Result) Summary() string {
	cost := ""
	if res.CostUSD > 0 {
		cost = fmt.Sprintf(" · $%.4f at API prices", res.CostUSD)
	}
	return fmt.Sprintf("%s · %d tool calls · %d input tokens (%d cached) · %d output tokens%s · prompt: %s",
		res.Backend, res.ToolCalls, res.InputTokens, res.CachedTokens, res.OutputTokens, cost, res.PromptSource)
}
