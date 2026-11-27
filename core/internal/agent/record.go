package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tool-call record: every call a run makes to the routine's tools (and
// to report_outcome), in order, with its arguments, what it returned or its
// error, when it was made and whether a dry run intercepted it, so a run's
// outcome can be traced to what the tools said. Results are clipped, per
// call and per run, and anything that looks like a credential is redacted
// before it's kept. Only the tools the app serves are recorded: an agent
// CLI's built-in tools (Read, Bash…) run inside the CLI, whose output
// doesn't carry them on every backend.

// Caps on what the record keeps of tools' results: each call's (its error
// too), and a run's in all; a result past them is clipped and says so.
const (
	CallResultCap = 4 << 10
	RunResultCap  = 64 << 10
)

// Call is one tool call a run made.
type Call struct {
	// Tool is the routine's name for it (server/tool when it names the
	// server); report_outcome for the routine's report.
	Tool string `json:"tool"`
	// Server is the server offering it (absent in records from before
	// they said, and for report_outcome).
	Server string         `json:"server,omitempty"`
	Args   map[string]any `json:"args"`
	// Write is whether the tool can change things (not read-only): such
	// calls are intercepted in dry runs, and are also listed in Writes.
	Write bool `json:"write"`
	// At is when it was made (absent in records from before it was kept).
	At *time.Time `json:"at,omitempty"`
	// Result is what the tool returned (its text; other content as a
	// placeholder), clipped (Truncated) past CallResultCap, or past what's
	// left of RunResultCap; absent when it failed or returned nothing.
	Result string `json:"result,omitempty"`
	// Error is the tool's error, when the call failed.
	Error string `json:"error,omitempty"`
	// Truncated says Result (or Error) was clipped; ResultSize is then its
	// size in bytes before it was.
	Truncated  bool `json:"truncated,omitempty"`
	ResultSize int  `json:"resultSize,omitempty"`
	// Intercepted is a write a dry run didn't make (it counts as done).
	Intercepted bool `json:"intercepted,omitempty"`

	ended bool // the call returned (live runs only)
}

// callEnd is how a call ended.
type callEnd struct {
	result, err string
	intercepted bool
	reauth      error
}

// startCall records a tool call as it starts, and returns where it's
// recorded for endCall.
func (r *Result) startCall(tool, server string, args map[string]any, isWrite bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	kept := redactArgs(args)
	r.ToolCalls++
	r.Calls = append(r.Calls, Call{Tool: tool, Server: server, Args: kept, Write: isWrite, At: &now})
	call := len(r.Calls) - 1
	if isWrite {
		r.Writes = append(r.Writes, Write{Tool: tool, Server: server, Args: kept})
		r.writeOf = append(r.writeOf, call)
	}
	return call
}

// endCall records how a call ended, and the first sign-in that failed
// during the run.
func (r *Result) endCall(call int, e callEnd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := &r.Calls[call]
	c.ended, c.Intercepted = true, e.intercepted
	if e.err != "" {
		text := redact(e.err)
		if len(text) > CallResultCap {
			c.Truncated, c.ResultSize = true, len(text)
			text = clip(text, CallResultCap)
		}
		c.Error = text
	} else if e.result != "" {
		text := redact(e.result)
		limit := min(CallResultCap, max(RunResultCap-r.kept, 0))
		if len(text) > limit {
			c.Truncated, c.ResultSize = true, len(text)
			text = clip(text, limit)
		}
		r.kept += len(text)
		c.Result = text
	}
	for i, at := range r.writeOf {
		if at == call {
			r.Writes[i].Error = c.Error
		}
	}
	if e.reauth != nil && r.reauth == nil {
		r.reauth = e.reauth
	}
}

// clip cuts s to at most n bytes, on a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// resultText is everything a tool returned, as text: its text content, a
// placeholder for anything else, or its structured content when it has
// nothing else.
func resultText(out *sdk.CallToolResult) string {
	if out == nil {
		return ""
	}
	var parts []string
	for _, c := range out.Content {
		switch c := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, c.Text)
		case *sdk.ImageContent:
			parts = append(parts, "[image "+c.MIMEType+"]")
		case *sdk.AudioContent:
			parts = append(parts, "[audio "+c.MIMEType+"]")
		case *sdk.ResourceLink:
			parts = append(parts, "[resource "+c.URI+"]")
		case *sdk.EmbeddedResource:
			if c.Resource != nil && c.Resource.Text != "" {
				parts = append(parts, c.Resource.Text)
			} else if c.Resource != nil {
				parts = append(parts, "[resource "+c.Resource.URI+"]")
			}
		default:
			parts = append(parts, fmt.Sprintf("[%T]", c))
		}
	}
	if len(parts) == 0 && out.StructuredContent != nil {
		if b, err := json.Marshal(out.StructuredContent); err == nil {
			return string(b)
		}
	}
	return strings.Join(parts, "\n")
}

// Redaction: the record never keeps what looks like a credential, should a
// tool echo one: tokens of well-known shapes, and the values of keys named
// like secrets.
var (
	secretKeyRE = regexp.MustCompile(`(?i)^(?:access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|token|api[_-]?key|apikey|` +
		`client[_-]?secret|secret|password|passwd|authorization|cookie|private[_-]?key)$`)
	secretPairRE = regexp.MustCompile(`(?i)(["']?\b(?:access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|token|api[_-]?key|apikey|` +
		`client[_-]?secret|secret|password|passwd|authorization)\b["']?\s*[:=]\s*["']?)(?:bearer\s+)?[^\s"',;&}]{6,}`)
	secretTokenREs = []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`),
		regexp.MustCompile(`\bxox[a-z]-[A-Za-z0-9-]{10,}`),
		regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`),
	}
)

// Redacted replaces a secret in the record.
const Redacted = "[redacted]"

// Redact replaces what looks like a credential in s, as the record does
// (logs exported for a bug report go through it too).
func Redact(s string) string { return redact(s) }

// redact replaces what looks like a credential in s.
func redact(s string) string {
	s = secretPairRE.ReplaceAllString(s, "${1}"+Redacted)
	for _, re := range secretTokenREs {
		s = re.ReplaceAllString(s, Redacted)
	}
	return s
}

// redactArgs is a copy of a call's arguments to keep: a value under a key
// named like a secret replaced, and strings redacted.
func redactArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if secretKeyRE.MatchString(k) {
			out[k] = Redacted
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch v := v.(type) {
	case string:
		return redact(v)
	case map[string]any:
		return redactArgs(v)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = redactValue(x)
		}
		return out
	}
	return v
}
