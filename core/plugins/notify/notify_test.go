package notify

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// host is a pluginapi.Host for one test: notify only asks the app's name.
type host struct{}

func (host) Connect(context.Context, string) (*sdk.ClientSession, error) {
	return nil, errors.New("none")
}
func (host) ConnectApp(context.Context) (*sdk.ClientSession, error) { return nil, errors.New("none") }
func (host) AppName() string                                        { return "Test App" }
func (host) Config(any) error                                       { return nil }
func (host) Secret(string) (string, error)                          { return "", errors.New("none") }
func (host) DataDir() (string, error)                               { return "", errors.New("none") }
func (host) RoutineConfig(any) error                                { return nil }
func (host) Run() pluginapi.Run                                     { return pluginapi.Run{} }

// notify_me shows the notification through the OS (replaced here: tests
// never show one), as the app, and refuses an empty title.
func TestNotifyMe(t *testing.T) {
	var shown []Note
	orig := Deliver
	Deliver = func(n Note) error { shown = append(shown, n); return nil }
	t.Cleanup(func() { Deliver = orig })

	srv, err := Plugin().Serve(host{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
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
	Deliver = func(Note) error { return errors.New("notifications are turned off") }
	if res := call(map[string]any{"title": "x"}); !res.IsError {
		t.Error("a failed delivery wasn't the model's to see")
	}
}
