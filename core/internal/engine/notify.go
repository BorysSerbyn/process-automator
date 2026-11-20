package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/notify"
	"github.com/BorysSerbyn/process-automator/core/internal/pluginapi"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NotifyServer is the core's notify plugin, for routines.
const NotifyServer = "notify"

// notifyPlugin is the core's own plugin routines use to tell me things
// (notify_me). It's the core's because it writes the core's notifications.
func notifyPlugin(l Layout) pluginapi.Plugin {
	return pluginapi.Plugin{
		Name:        NotifyServer,
		Description: "Lets routines notify me (notify_me), delivered by automator-ui",
		Serve: func(h pluginapi.Host) (*sdk.Server, error) {
			run := h.Run()
			path := l.Notifications()
			s := sdk.NewServer(&sdk.Implementation{Name: NotifyServer, Version: "1"}, nil)
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
				n, err := notify.Add(path, notify.Notification{
					Kind: notify.KindRoutine, Title: args.Title, Body: args.Body, Urgent: args.Urgent,
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

// notifyFailure records a notification for a run that failed, needs a
// sign-in, or was skipped or missed (unless notifications.failures is off).
func notifyFailure(a *App, routine string, run runstatus.Run) {
	var title string
	switch run.Outcome {
	case runstatus.OutcomeFailed:
		title = routine + " failed"
	case runstatus.OutcomeNeedsReauth:
		title = routine + " needs you to sign in again"
	case runstatus.OutcomeSkipped:
		title = routine + " was skipped"
	case runstatus.OutcomeMissed:
		title = routine + " missed its run"
		if run.For != nil {
			title = fmt.Sprintf("%s missed its %s run", routine, run.For.Local().Format("15:04"))
		}
	default:
		return
	}
	if vals, err := a.Settings(); err == nil && !vals.Bool("notifications.failures") {
		return
	}
	body := run.Reason
	if r := []rune(body); len(r) > 240 {
		body = string(r[:240]) + "…"
	}
	_, err := notify.Add(a.Layout.Notifications(), notify.Notification{
		Kind: notify.KindFailure, Title: title, Body: body,
		Routine: routine, Run: history.RunID(routine, run.StartedAt),
	}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not record a notification:", err)
	}
}
