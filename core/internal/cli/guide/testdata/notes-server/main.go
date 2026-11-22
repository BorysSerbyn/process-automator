// The notes server: a minimal MCP server over stdio, with one read-only tool
// and one write tool, to install as a plugin folder.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type note struct {
	Added time.Time `json:"added"`
	Text  string    `json:"text"`
}

type notes struct {
	Notes []note `json:"notes"`
}

type addInput struct {
	Text string `json:"text" jsonschema:"the note"`
}

func main() {
	file := os.Getenv("NOTES_FILE") // set in plugin.yaml's env
	server := mcp.NewServer(&mcp.Implementation{Name: "notes", Version: "1.0.0"}, nil)

	// Read-only: dry runs call it for real.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_notes",
		Description: "List my notes, oldest first, with when each was added.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, notes, error) {
		all, err := load(file)
		return nil, notes{Notes: all}, err
	})

	// A write: dry runs intercept it.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_note",
		Description: "Add a note.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, notes, error) {
		all, err := load(file)
		if err != nil {
			return nil, notes{}, err
		}
		all = append(all, note{Added: time.Now(), Text: in.Text})
		b, err := json.Marshal(all)
		if err != nil {
			return nil, notes{}, err
		}
		return nil, notes{Notes: all}, os.WriteFile(file, b, 0o600)
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// load reads the notes file; there are none before the first one is added.
func load(file string) ([]note, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []note
	return all, json.Unmarshal(b, &all)
}
