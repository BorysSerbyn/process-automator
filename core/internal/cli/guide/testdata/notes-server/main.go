// A minimal MCP server over stdio: one read-only tool, one write.
package main

import (
	"context"
	"log"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type addInput struct {
	Text string `json:"text" jsonschema:"the note"`
}

func main() {
	var notes []string
	s := mcp.NewServer(&mcp.Implementation{Name: "notes", Version: "1.0.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "list_notes", Description: "List my notes.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, // a read: dry runs call it
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(notes, "\n")}}}, nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "add_note", Description: "Add a note.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false}}, // a write: dry runs intercept it
		func(_ context.Context, _ *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, any, error) {
			notes = append(notes, in.Text)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "added"}}}, nil, nil
		})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
