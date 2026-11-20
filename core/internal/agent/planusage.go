package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

// PlanUsage asks Claude Code for the plan's usage: `claude -p /usage` with
// stream-json output carries it as a usage_report (documented in the Agent
// SDK as experimental). /usage is a local command: no model call, no tokens.
// It needs the claude.ai login (a setup token gets no report).
func (b *ClaudeCode) PlanUsage(ctx context.Context) ([]usage.Limit, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "procauto-usage-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cmd := exec.CommandContext(ctx, b.Path, "-p", "/usage",
		"--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--setting-sources", "", "--strict-mcp-config", "--tools", "", "--restricted")
	cmd.Dir, cmd.Env = dir, b.env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude /usage: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseUsageReport(out)
}

// parseUsageReport reads the usage report's limit rows out of stream-json
// output, by kind; unknown fields and kinds are left alone.
func parseUsageReport(stream []byte) ([]usage.Limit, error) {
	sc := bufio.NewScanner(bytes.NewReader(stream))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line struct {
			UsageReport *struct {
				RateLimits struct {
					Limits []struct {
						Kind     string   `json:"kind"`
						Percent  *float64 `json:"percent"`
						ResetsAt string   `json:"resets_at"`
						Scope    *struct {
							Model *struct {
								DisplayName string `json:"display_name"`
							} `json:"model"`
						} `json:"scope"`
					} `json:"limits"`
				} `json:"rate_limits"`
			} `json:"usage_report"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.UsageReport == nil {
			continue
		}
		var limits []usage.Limit
		for _, l := range line.UsageReport.RateLimits.Limits {
			at, err := time.Parse(time.RFC3339Nano, l.ResetsAt)
			if l.Percent == nil || err != nil {
				continue
			}
			u := usage.Limit{Kind: l.Kind, Percent: *l.Percent, ResetsAt: at}
			if l.Scope != nil && l.Scope.Model != nil {
				u.Model = l.Scope.Model.DisplayName
			}
			limits = append(limits, u)
		}
		for _, l := range limits {
			if l.Kind == "session" || l.Kind == "weekly_all" {
				return limits, nil
			}
		}
		return nil, errors.New("claude /usage gave a usage report without a 5-hour or weekly limit this app can read (its format may have changed)")
	}
	return nil, errors.New("claude /usage gave no usage report (it needs Claude Code signed in with your claude.ai account)")
}
