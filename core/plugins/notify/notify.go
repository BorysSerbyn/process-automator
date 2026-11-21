// Package notify is the notify plugin: routines that list it in
// automator.servers can notify me (notify_me). It's a plugin like any other,
// built only on pluginapi, for apps that list it in their plugins. It shows
// its notifications itself, through the OS (macOS: UserNotifications, as the
// app; Linux: the freedesktop notification service). Clicking one does
// nothing.
package notify

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Name is the plugin's name, as routines list it in automator.servers.
const Name = "notify"

// Note is one notification to show.
type Note struct {
	// App is the app's name, the notification's sender where the OS asks for
	// one (Linux; macOS takes the app bundle's).
	App         string
	Title, Body string
	// Urgent breaks through focus modes where the OS allows (macOS
	// time-sensitive, Linux critical urgency).
	Urgent bool
}

// Deliver shows a notification through the OS. Tests replace it: they never
// show real notifications.
var Deliver = deliver

// Plugin is the notify plugin.
func Plugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Name:        Name,
		Description: "Lets routines notify me (notify_me) with a desktop notification",
		Serve: func(h pluginapi.Host) (*sdk.Server, error) {
			s := sdk.NewServer(&sdk.Implementation{Name: Name, Version: "1"}, nil)
			type in struct {
				Title  string `json:"title" jsonschema:"short, like a headline"`
				Body   string `json:"body,omitempty" jsonschema:"a sentence or two"`
				Urgent bool   `json:"urgent,omitempty" jsonschema:"only for something I must see now: it breaks through focus modes"`
			}
			sdk.AddTool(s, &sdk.Tool{
				Name: "notify_me",
				Description: "Notify me with a desktop notification. Keep it brief; use it for what needs my attention, " +
					"not for routine progress. Clicking it does nothing, so say what to look at.",
				Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), OpenWorldHint: new(false)},
			}, func(_ context.Context, _ *sdk.CallToolRequest, args in) (*sdk.CallToolResult, any, error) {
				if strings.TrimSpace(args.Title) == "" {
					return nil, nil, errors.New("title is required")
				}
				if err := Deliver(Note{App: h.AppName(), Title: args.Title, Body: args.Body, Urgent: args.Urgent}); err != nil {
					return nil, nil, err
				}
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "Shown."}}}, nil, nil
			})
			return s, nil
		},
	}
}
