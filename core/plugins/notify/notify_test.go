package notify

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
	"github.com/BorysSerbyn/process-automator/core/pluginapi/plugintest"
)

// notify_me hands every notification to the app (which shows it as one of
// its own about the run), and refuses an empty title, or an acknowledgeable
// one without a body.
func TestNotifyMe(t *testing.T) {
	h := &plugintest.Host{}
	cs := plugintest.Serve(t, Plugin(), h)
	call := func(args map[string]any) *sdk.CallToolResult {
		res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "notify_me", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(map[string]any{"title": "3 open threads", "body": "see the thread", "urgent": true}); res.IsError {
		t.Fatalf("plain: %+v", res.Content)
	}
	if res := call(map[string]any{"title": "Ana asked for the report", "body": "in #team", "acknowledge": true}); res.IsError {
		t.Fatalf("to acknowledge: %+v", res.Content)
	}
	if res := call(map[string]any{"title": "PR reviewed", "url": "https://github.com/o/r/pull/1"}); res.IsError {
		t.Fatalf("with a link: %+v", res.Content)
	}
	want := []pluginapi.Notice{
		{Title: "3 open threads", Body: "see the thread", Urgent: true},
		{Title: "Ana asked for the report", Body: "in #team", Acknowledge: true},
		{Title: "PR reviewed", URL: "https://github.com/o/r/pull/1"},
	}
	if len(h.Notified) != len(want) {
		t.Fatalf("notified %+v", h.Notified)
	}
	for i := range want {
		if h.Notified[i] != want[i] {
			t.Errorf("notified[%d] = %+v, want %+v", i, h.Notified[i], want[i])
		}
	}
	if res := call(map[string]any{"title": " "}); !res.IsError {
		t.Error("an empty title was accepted")
	}
	if res := call(map[string]any{"title": "Ana asked", "acknowledge": true}); !res.IsError {
		t.Error("one to acknowledge without a body was accepted")
	}
	if len(h.Notified) != len(want) {
		t.Errorf("a refused one reached the app: %+v", h.Notified)
	}
}
