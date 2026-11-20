package pluginapi

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolCaller is the part of a client session plugins need to call tools.
// *sdk.ClientSession satisfies it; tests substitute fakes.
type ToolCaller interface {
	CallTool(ctx context.Context, params *sdk.CallToolParams) (*sdk.CallToolResult, error)
}

// CallJSON calls a tool and decodes its text result as JSON into out.
// A tool-level error (IsError) is returned as an error with the tool's message.
func CallJSON(ctx context.Context, c ToolCaller, tool string, args map[string]any, out any) error {
	res, err := c.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	text := ResultText(res)
	if res.IsError {
		return fmt.Errorf("%s: %s", tool, text)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("%s: decode result: %w", tool, err)
	}
	return nil
}

// ResultText returns the first text block of a tool result.
func ResultText(res *sdk.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*sdk.TextContent); ok {
			return t.Text
		}
	}
	return ""
}
