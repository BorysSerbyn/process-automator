package notify

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
	"github.com/BorysSerbyn/process-automator/core/pluginapi/plugintest"
)

// notify_me shows the notification through the OS (replaced here: tests
// never show one), as the app, and refuses an empty title.
func TestNotifyMe(t *testing.T) {
	var shown []Note
	orig := Deliver
	Deliver = func(n Note) error { shown = append(shown, n); return nil }
	t.Cleanup(func() { Deliver = orig })

	cs := plugintest.Serve(t, Plugin(), &plugintest.Host{})
	ctx := context.Background()
	call := func(args map[string]any) *sdk.CallToolResult {
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "notify_me", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(map[string]any{"title": "3 open threads", "body": "see the thread", "urgent": true}); res.IsError {
		t.Fatalf("notify_me: %+v", res.Content)
	}
	want := Note{App: "Test App", Title: "3 open threads", Body: "see the thread", Urgent: true}
	if len(shown) != 1 || shown[0] != want {
		t.Fatalf("shown %+v", shown)
	}
	if res := call(map[string]any{"title": " "}); !res.IsError || len(shown) != 1 {
		t.Error("an empty title was shown")
	}
	// One to acknowledge is the app's to show: handed to it, not shown here.
	h := &plugintest.Host{}
	acs := plugintest.Serve(t, Plugin(), h)
	res, err := acs.CallTool(ctx, &sdk.CallToolParams{Name: "notify_me",
		Arguments: map[string]any{"title": "Ana asked for the report", "body": "in #team", "acknowledge": true}})
	if err != nil || res.IsError {
		t.Fatal(res, err)
	}
	if len(shown) != 1 || len(h.Asked) != 1 || h.Asked[0] != (pluginapi.Notice{Title: "Ana asked for the report", Body: "in #team"}) {
		t.Fatalf("shown %+v, asked %+v", shown, h.Asked)
	}
	Deliver = func(Note) error { return errors.New("notifications are turned off") }
	if res := call(map[string]any{"title": "x"}); !res.IsError {
		t.Error("a failed delivery wasn't the model's to see")
	}
}
