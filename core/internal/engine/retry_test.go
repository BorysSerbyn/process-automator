package engine

import (
	"errors"
	"fmt"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
)

// A run the computer's sleep cut off runs again only when that can't do
// anything twice.
func TestSafeToRetryOnlyWhenNothingWasChanged(t *testing.T) {
	dropped := errors.New("connection reset by peer")
	for _, c := range []struct {
		name     string
		res      *agent.Result
		builtins []string
		err      error
		want     bool
	}{
		{"failed before any call", nil, nil, dropped, true},
		{"only reads", &agent.Result{Calls: []agent.Call{{Tool: "list"}}}, nil, dropped, true},
		{"wrote something", &agent.Result{Writes: []agent.Write{{Tool: "post"}}}, nil, dropped, false},
		{"agent CLI tools", nil, []string{"Bash"}, dropped, false},
		{"needs a sign-in", nil, nil, fmt.Errorf("chrono: %w", mcp.ErrNeedsReauth), false},
		{"plan limit", nil, nil, &agent.PlanLimitError{}, false},
	} {
		if got := safeToRetry(c.res, c.builtins, c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
