// Package notify is the notify plugin: routines that list it in
// automator.servers can notify me (notify_me). It's a plugin like any other,
// built only on pluginapi, for apps that list it in their plugins. Every
// notification is handed to the app (pluginapi.Host.Notify), which shows it
// as one of its own about the run: a click opens its link (url) or else the
// run, and one to acknowledge has an Acknowledge button and keeps the app
// red until I acknowledge it.
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

// Plugin is the notify plugin.
func Plugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Name:        Name,
		Description: "Lets routines notify me (notify_me) with a desktop notification",
		// macOS shows them only while the app may notify; routines using it
		// can't work, and say so, until it may.
		Permissions: []string{"notifications"},
		InProcess:   &pluginapi.InProcess{Serve: serve},
	}
}

func serve(h pluginapi.Host) (*sdk.Server, error) {
	s := sdk.NewServer(&sdk.Implementation{Name: Name, Version: "1"}, nil)
	type in struct {
		Title       string `json:"title" jsonschema:"short, like a headline"`
		Body        string `json:"body,omitempty" jsonschema:"a sentence or two; required with acknowledge, where it may be long (a whole report): the banner shows its start, the app's Acknowledgements window all of it"`
		URL         string `json:"url,omitempty" jsonschema:"a link (http or https) it's about, such as a pull request: clicking the notification opens it"`
		Urgent      bool   `json:"urgent,omitempty" jsonschema:"only for something I must see now: it breaks through focus modes"`
		Acknowledge bool   `json:"acknowledge,omitempty" jsonschema:"true when I must act on it, e.g. someone asked me for something: it waits, with an Acknowledge button, and the app's status stays red until I acknowledge it"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "notify_me",
		Description: "Notify me with a desktop notification. Keep it brief; use it for what needs my attention, " +
			"not for routine progress. Give url when it's about something with a link (a pull request, a thread): " +
			"clicking the notification opens it; without one, clicking opens this run's details. " +
			"Set acknowledge for what I must act on and mustn't miss (one notification per thing, e.g. each request " +
			"someone made of me): it waits for me to acknowledge it, and the app shows red until I do; " +
			"plain notifications are for what's only worth knowing.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), OpenWorldHint: new(false)},
	}, func(_ context.Context, _ *sdk.CallToolRequest, args in) (*sdk.CallToolResult, any, error) {
		if strings.TrimSpace(args.Title) == "" {
			return nil, nil, errors.New("title is required")
		}
		if args.Acknowledge && strings.TrimSpace(args.Body) == "" {
			return nil, nil, errors.New("body is required with acknowledge: say what it's about, in full if need be")
		}
		id, err := h.Notify(pluginapi.Notice{Title: args.Title, Body: args.Body, URL: args.URL, Urgent: args.Urgent, Acknowledge: args.Acknowledge})
		if err != nil {
			return nil, nil, err
		}
		text := "Shown; clicking it opens the run (notification " + id + ")."
		switch {
		case args.Acknowledge:
			text = "Shown; it waits for me to acknowledge it (notification " + id + ")."
		case args.URL != "":
			text = "Shown; clicking it opens the link (notification " + id + ")."
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
	})
	return s, nil
}
