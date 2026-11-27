package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// A server that never answers fails the connect, saying so, instead of
// hanging whatever needed it.
func TestConnectGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	orig := ConnectTimeout
	ConnectTimeout = 300 * time.Millisecond
	t.Cleanup(func() { ConnectTimeout = orig })
	c := &Connector{Servers: map[string]pluginapi.ServerSpec{"stuck": {Command: "sleep", Args: []string{"60"}}}}
	_, err := c.Connect(context.Background(), "stuck")
	if err == nil || !strings.Contains(err.Error(), "connect stuck: no answer after 300ms") {
		t.Fatalf("err = %v", err)
	}
}

// Ended by its caller, a connect says so rather than that it got no answer.
func TestACancelledConnectSaysSo(t *testing.T) {
	c := &Connector{Servers: map[string]pluginapi.ServerSpec{"stuck": {Command: "sleep", Args: []string{"60"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := c.Connect(ctx, "stuck")
	if err == nil || strings.Contains(err.Error(), "no answer") {
		t.Fatalf("err = %v", err)
	}
}
