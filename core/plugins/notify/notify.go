// Package notify is the notify plugin: routines that list it in
// automator.servers can notify me (notify_me). It's a plugin like any other,
// shipped with every app built on the engine because what it records is
// delivered by the app's frontends. Its settings are each routine's
// (routines.<name>.onNotificationClick); the app's own notifications (runs
// that failed, updates) never go through it.
package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	notifications "github.com/BorysSerbyn/process-automator/core/internal/notify"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Name is the plugin's name, as routines list it in automator.servers.
const Name = "notify"

// Plugin is the notify plugin; it records what routines ask in the app's
// notifications file at path, for its frontends to deliver.
func Plugin(path string) pluginapi.Plugin {
	return pluginapi.Plugin{
		Name:        Name,
		Description: "Lets routines notify me (notify_me), delivered by automator-ui",
		Serve: func(h pluginapi.Host) (*sdk.Server, error) {
			run := h.Run()
			s := sdk.NewServer(&sdk.Implementation{Name: Name, Version: "1"}, nil)
			type in struct {
				Title  string `json:"title" jsonschema:"short, like a headline"`
				Body   string `json:"body,omitempty" jsonschema:"a sentence or two"`
				Open   string `json:"open,omitempty" jsonschema:"a file (or folder) to open when I click it, e.g. a report this run wrote; relative paths are in the routine's folder. Not an app or script"`
				Urgent bool   `json:"urgent,omitempty" jsonschema:"only for something I must see now: it breaks through focus modes"`
			}
			sdk.AddTool(s, &sdk.Tool{
				Name: "notify_me",
				Description: "Notify me with a desktop notification. Keep it brief; use it for what needs my attention, " +
					"not for routine progress.",
				Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), OpenWorldHint: new(false)},
			}, func(_ context.Context, _ *sdk.CallToolRequest, args in) (*sdk.CallToolResult, any, error) {
				if strings.TrimSpace(args.Title) == "" {
					return nil, nil, errors.New("title is required")
				}
				open := ""
				if args.Open != "" {
					var err error
					if open, err = openable(args.Open, run.Folder); err != nil {
						return nil, nil, err
					}
				}
				n, err := notifications.Add(path, notifications.Notification{
					Kind: notifications.KindRoutine, Title: args.Title, Body: args.Body, Urgent: args.Urgent,
					Routine: run.Routine, Run: run.ID, Open: open,
				}, time.Now())
				if err != nil {
					return nil, nil, err
				}
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "Recorded notification " + n.ID + "."}}}, nil, nil
			})
			return s, nil
		},
	}
}

// openable checks a file a routine wants opened on click: it must exist and be
// a document or folder, never something opening would run (an app, a script,
// an executable), so a click can't run code a routine wrote.
func openable(path, folder string) (string, error) {
	if !filepath.IsAbs(path) {
		if folder == "" {
			return "", errors.New("open: a relative path needs the routine's folder (automator.folder)")
		}
		path = filepath.Join(folder, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	if launches(path, info) {
		return "", fmt.Errorf("open: opening %s would run something; give a document or folder", filepath.Base(path))
	}
	return path, nil
}
