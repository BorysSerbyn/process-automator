// Package agent runs a routine prompt with tools. The engine side is the same for
// every model backend: it connects the routine's MCP servers, exposes exactly the
// allowed tools through a gateway (which logs calls, records writes and intercepts
// them in dry-run), and hands prompt + tools to a Backend that runs the model loop.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/firebase/genkit/go/genkit"
	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// MaxTurns bounds the model/tool loop of one run.
const MaxTurns = 30

// Agent runs prompts.
type Agent struct {
	Connector *mcp.Connector
	// Backend runs the model loop (Genkit + API key, or an agent CLI).
	Backend Backend
	// DryRun intercepts every tool not declared read-only by its server.
	DryRun bool
	// Log receives progress lines (tool calls); nil discards them.
	Log io.Writer
}

// Request is one prompt run.
type Request struct {
	Name   string // prompt name, e.g. "standup"
	Source string // Dotprompt source
	Origin string // where the source came from (reported back)
	Input  map[string]any
	// Tools is the allowlist: only these tools reach the model.
	Tools []string
	// Servers are the MCP servers the tools come from.
	Servers []string
	// Run is the routine run in-process servers serve (see mcp.Run).
	Run mcp.Run
	// Partials are snippets available to the prompt as {{> name}}.
	Partials map[string]string
	// Effort is how hard the model thinks (low … max); "" or "default" leaves
	// it to the backend.
	Effort string
	// Environment, for agent CLI backends (others reject them): Folder is
	// where the run works ("" for a temporary folder); SaveSession keeps the
	// session, under SessionID for a backend that takes one (see
	// SessionPicker); BuiltinTools are the CLI's own tools allowed (in a dry
	// run only those that can't change anything are given).
	Folder       string
	SaveSession  bool
	SessionID    string
	BuiltinTools []string
	// Contract is the routine's outcome contract (automator.outcome): how
	// it's told to report, and what its report is held to.
	Contract Contract
}

// Write is a side-effecting tool call (intercepted in dry-run).
type Write struct {
	// Tool is the routine's name for it (server/tool when it names the server).
	Tool string `json:"tool"`
	// Server is the server offering it (absent in records from before
	// they said).
	Server string         `json:"server,omitempty"`
	Args   map[string]any `json:"args"`
	// Error is the tool's error, when the call failed.
	Error string `json:"error,omitempty"`
}

// Result of a run.
type Result struct {
	Text   string  `json:"text"`
	Writes []Write `json:"writes"`
	// Calls are every tool call, in order (absent in records from before
	// they were kept).
	Calls   []Call `json:"calls,omitempty"`
	DryRun  bool   `json:"dryRun"`
	Backend string `json:"backend"`
	// SessionID is the session the run kept (session save), as the backend
	// reports it; the caller records it with the run.
	SessionID    string `json:"-"`
	ToolCalls    int    `json:"toolCalls"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	CachedTokens int    `json:"cachedTokens"`
	// CostUSD is the run's cost at API list prices: claude-api runs only (agent CLI
	// runs are on the CLI's own plan, and the app never works out a cost for them).
	CostUSD      float64 `json:"costUsd,omitempty"`
	PromptSource string  `json:"promptSource"` // the routine file
	// Output is the result in the shape the routine declared (Dotprompt
	// output: schema), validated; absent for routines without one.
	Output any `json:"output,omitempty"`
	// Outcome is the routine's own report when the run succeeded: done or
	// nothing_to_do (a failed report fails the run). Reason says why.
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Case is the nothing-to-do case a nothing_to_do report cited (one of
	// the routine's automator.outcome.nothingToDo); Evidence is what the
	// report gave as showing its outcome.
	Case     string `json:"case,omitempty"`
	Evidence string `json:"evidence,omitempty"`

	reported *reported // set by the report_outcome tool
	reauth   error     // a sign-in that failed during the run
	writeOf  []int     // the call each of Writes is
	kept     int       // bytes of results the record keeps so far

	// mu guards what tool calls record: a backend may run them concurrently
	// (agent CLI backends serve them over HTTP, each in its own goroutine).
	mu sync.Mutex
}

// report records the routine's own outcome (report_outcome).
func (r *Result) report(rep *reported) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reported = rep
}

// Backend runs the model/tool loop for one job.
type Backend interface {
	Name() string
	Run(ctx context.Context, j Job) (*Output, error)
}

// Job is what a backend gets: the prompt (to render with Genkit/Dotprompt) and
// the gateway tools. Tools already enforce logging and dry-run.
type Job struct {
	Name     string
	Source   string
	Input    map[string]any
	Partials map[string]string
	Tools    []Tool
	MaxTurns int
	// Instructions are added to the routine's system prompt by every backend
	// (e.g. how to report the outcome).
	Instructions string
	// Effort is the routine's effort, "" when left to the backend.
	Effort string
	// Folder, SaveSession, SessionID and BuiltinTools are the run's
	// environment (see Request); DryRun says built-ins that write must be withheld.
	Folder       string
	SaveSession  bool
	SessionID    string
	BuiltinTools []string
	DryRun       bool
}

// Output is what a backend returns.
type Output struct {
	Text                                    string
	InputTokens, OutputTokens, CachedTokens int
	CostUSD                                 float64
	// Model is set by backends that price runs themselves (from their
	// tokens); Priced says whether its price was known.
	Model  string
	Priced bool
	// Schema is the routine's output schema (Dotprompt output:), when it
	// declares one; Structured is the result in that shape (validated by the agent).
	Schema     map[string]any
	Structured any
	// SessionID is the session the run kept (Job.SaveSession), "" when none.
	SessionID string
}

// Tool is an allowed MCP tool behind the gateway.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Annotations *sdk.ToolAnnotations
	// Call runs the tool (or intercepts it in dry-run). A tool-level failure is a
	// result with IsError set; err is for transport failures.
	Call func(ctx context.Context, args map[string]any) (*sdk.CallToolResult, error)
}

// Run executes the request.
func (a *Agent) Run(ctx context.Context, req Request) (*Result, error) {
	if a.Backend == nil {
		return nil, errors.New("no model backend configured")
	}
	res := &Result{DryRun: a.DryRun, PromptSource: req.Origin, Backend: a.Backend.Name(), Writes: []Write{}, Calls: []Call{}}
	tools, sessions, err := a.gateway(ctx, req.Run, req.Servers, req.Tools, res)
	defer closeSessions(sessions)
	if err != nil {
		return nil, err
	}
	out, err := a.Backend.Run(ctx, Job{
		Name: req.Name, Source: req.Source, Input: req.Input,
		Partials: req.Partials, Tools: append(tools, outcomeTool(res, req.Contract)), MaxTurns: MaxTurns,
		Instructions: req.Contract.Instructions(), Effort: effort(req.Effort),
		Folder: req.Folder, SaveSession: req.SaveSession, SessionID: req.SessionID, BuiltinTools: req.BuiltinTools, DryRun: a.DryRun,
	})
	if out != nil {
		res.Text, res.SessionID = out.Text, out.SessionID
		res.InputTokens, res.OutputTokens, res.CachedTokens = out.InputTokens, out.OutputTokens, out.CachedTokens
		res.CostUSD = out.CostUSD
		if out.Model != "" && !out.Priced {
			a.logf("warning: no price known for %s: this run's cost isn't counted toward the LLM budgets", out.Model)
		}
	}
	if err != nil {
		return res, err
	}
	if out != nil && out.Schema != nil {
		if err := checkOutput(out.Schema, out.Structured); err != nil {
			return res, err
		}
		res.Output = out.Structured
	}
	return res, verdict(res, req.Contract)
}

// discovered is an allowlisted tool and the server session offering it.
type discovered struct {
	server  string
	session *sdk.ClientSession
	tool    *sdk.Tool
	// entry is the allowlist's name for it: the tool's, or server/tool.
	entry string
}

// ToolsError is a routine's tools not matching what its servers offer (one
// none offers, or one offered by two): the servers answered, so the list is
// wrong, unlike a server that can't be reached or isn't signed in.
type ToolsError struct{ msg string }

func (e *ToolsError) Error() string { return e.msg }

// SplitTool splits an allowlist entry: a tool's name, offered by exactly one
// of the routine's servers, or server/tool, naming the server too (for a
// tool two of them offer, such as two accounts of one plugin).
func SplitTool(entry string) (server, tool string) {
	if s, t, ok := strings.Cut(entry, "/"); ok {
		return s, t
	}
	return "", entry
}

// ModelName is the name the model calls an allowlisted tool by: the tool's
// own, or for server/tool, server__tool, which says the server (tool names
// can't hold a slash).
func ModelName(entry string) string {
	if server, tool := SplitTool(entry); server != "" {
		return server + "__" + tool
	}
	return entry
}

// CheckToolNames checks a routine's allowlist against its servers without
// connecting to them: each server/tool names one of the servers, and no
// tool is listed twice.
func CheckToolNames(servers, allowed []string) error {
	seen := map[string]bool{}
	for _, e := range allowed {
		server, tool := SplitTool(e)
		switch {
		case tool == "" || strings.Contains(tool, "/") || (server == "" && strings.HasPrefix(e, "/")):
			return fmt.Errorf("tool %q: name a tool, or server/tool", e)
		case server != "" && !slices.Contains(servers, server):
			return fmt.Errorf("tool %q: %s isn't one of its servers", e, server)
		case seen[ModelName(e)]:
			return fmt.Errorf("tool %q is listed twice", e)
		}
		seen[ModelName(e)] = true
	}
	return nil
}

// discover connects to the servers and finds each allowed tool, by its
// model name (ModelName). An unqualified name must be offered by exactly one
// server; server/tool by that server. Sessions are returned even on error,
// for the caller to close.
func discover(ctx context.Context, conn *mcp.Connector, run mcp.Run, servers, allowed []string) (map[string]discovered, []*sdk.ClientSession, error) {
	if err := CheckToolNames(servers, allowed); err != nil {
		return nil, nil, &ToolsError{err.Error()}
	}
	bare := map[string]bool{}                 // unqualified entries
	qualified := map[string]map[string]bool{} // server -> tools named server/tool
	for _, e := range allowed {
		server, tool := SplitTool(e)
		if server == "" {
			bare[tool] = true
			continue
		}
		if qualified[server] == nil {
			qualified[server] = map[string]bool{}
		}
		qualified[server][tool] = true
	}
	byName := map[string]discovered{}
	offeredBy := map[string][]string{} // unqualified tool -> servers offering it
	var sessions []*sdk.ClientSession
	for _, name := range servers {
		s, err := conn.ConnectFor(ctx, name, run)
		if err != nil {
			return nil, sessions, err
		}
		sessions = append(sessions, s)
		list, err := s.ListTools(ctx, nil)
		if err != nil {
			return nil, sessions, fmt.Errorf("%s: list tools: %w", name, err)
		}
		for _, t := range list.Tools {
			if bare[t.Name] {
				offeredBy[t.Name] = append(offeredBy[t.Name], name)
				byName[t.Name] = discovered{name, s, t, t.Name}
			}
			if qualified[name][t.Name] {
				e := name + "/" + t.Name
				byName[ModelName(e)] = discovered{name, s, t, e}
			}
		}
	}
	for tool, by := range offeredBy {
		if len(by) > 1 {
			var q []string
			for _, s := range by {
				q = append(q, s+"/"+tool)
			}
			return nil, sessions, &ToolsError{fmt.Sprintf("tool %q is offered by more than one server (%s): name it with its server: %s",
				tool, strings.Join(by, " and "), strings.Join(q, " or "))}
		}
	}
	var missing []string
	for _, e := range allowed {
		if _, ok := byName[ModelName(e)]; !ok {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, sessions, &ToolsError{fmt.Sprintf("prompt lists tools no connected server offers: %s (missing sign-in or scopes? see `"+product.Command+" mcp tools <server>`)", strings.Join(missing, ", "))}
	}
	return byName, sessions, nil
}

// CheckedTool is an allowlisted tool as a routine would get it.
type CheckedTool struct {
	// Name is the allowlist's: the tool's, or server/tool.
	Name   string `json:"name"`
	Server string `json:"server"`
	// ModelName is the name the model calls it by, when it isn't Name
	// (server__tool for server/tool).
	ModelName string `json:"modelName,omitempty"`
	ReadOnly  bool   `json:"readOnly"`
}

// CheckTools connects to the servers and confirms each allowed tool is offered
// by exactly one of them (or, named server/tool, by that server), as a run
// would, without calling the model or any tool.
func CheckTools(ctx context.Context, conn *mcp.Connector, run mcp.Run, servers, allowed []string) ([]CheckedTool, error) {
	found, sessions, err := discover(ctx, conn, run, servers, allowed)
	closeSessions(sessions)
	if err != nil {
		return nil, err
	}
	out := make([]CheckedTool, 0, len(found))
	for name, f := range found {
		c := CheckedTool{Name: f.entry, Server: f.server, ReadOnly: f.tool.Annotations != nil && f.tool.Annotations.ReadOnlyHint}
		if name != f.entry {
			c.ModelName = name
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// gateway connects to the servers and wraps each allowed tool (see discover).
func (a *Agent) gateway(ctx context.Context, run mcp.Run, servers, allowed []string, res *Result) ([]Tool, []*sdk.ClientSession, error) {
	byName, sessions, err := discover(ctx, a.Connector, run, servers, allowed)
	if err != nil {
		return nil, sessions, err
	}
	runCtx := ctx

	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	tools := make([]Tool, 0, len(names))
	for _, name := range names {
		f := byName[name]
		schema, err := toMap(f.tool.InputSchema)
		if err != nil {
			return nil, sessions, err
		}
		readOnly := f.tool.Annotations != nil && f.tool.Annotations.ReadOnlyHint
		session, toolName, entry, server := f.session, f.tool.Name, f.entry, f.server
		desc := f.tool.Description
		if entry != toolName { // named with its server: say which it is
			desc = strings.TrimSpace(fmt.Sprintf("[%s] %s", server, desc))
		}
		tools = append(tools, Tool{
			Name: name, Description: desc, InputSchema: schema, Annotations: f.tool.Annotations,
			Call: func(ctx context.Context, args map[string]any) (*sdk.CallToolResult, error) {
				// A call ends with the run, wherever it was asked from: an
				// agent CLI's calls come over a connection of their own, which
				// a killed CLI doesn't end.
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				defer context.AfterFunc(runCtx, cancel)()
				a.logf("→ %s %s", entry, compact(args))
				call := res.startCall(entry, server, args, !readOnly)
				if !readOnly && a.DryRun {
					const intercepted = "DRY RUN: this call was not executed. Treat it as successful and finish."
					res.endCall(call, callEnd{result: intercepted, intercepted: true})
					return textResult(intercepted, false), nil
				}
				out, err := session.CallTool(ctx, &sdk.CallToolParams{Name: toolName, Arguments: args})
				switch {
				case err != nil:
					var reauth error
					if errors.Is(err, mcp.ErrNeedsReauth) {
						reauth = err
					}
					res.endCall(call, callEnd{err: err.Error(), reauth: reauth})
					return nil, fmt.Errorf("%s: %w", entry, err)
				case out.IsError:
					res.endCall(call, callEnd{err: firstNonEmpty(resultText(out), "the tool reported an error")})
				default:
					res.endCall(call, callEnd{result: resultText(out)})
				}
				return out, nil
			},
		})
	}
	return tools, sessions, nil
}

// closeGrace bounds closing a run's sessions: closing one waits for the
// calls its server is still serving, and one that ignores being cancelled
// (a plugin waiting on something that never answers) mustn't keep the run
// from ending.
var closeGrace = 10 * time.Second

// closeSessions closes the sessions, giving up on them after closeGrace.
func closeSessions(sessions []*sdk.ClientSession) {
	if len(sessions) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, s := range sessions {
			_ = s.Close()
		}
	}()
	select {
	case <-done:
	case <-time.After(closeGrace):
	}
}

// newGenkit creates a Genkit instance with the job's partials. Every backend
// renders prompts through Genkit's Dotprompt, so routine files mean the same thing
// whichever backend runs them.
func newGenkit(ctx context.Context, j Job, opts ...genkit.GenkitOption) *genkit.Genkit {
	g := genkit.Init(ctx, opts...)
	for name, src := range j.Partials {
		genkit.DefinePartial(g, name, src)
	}
	return g
}

func textResult(text string, isError bool) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, IsError: isError}
}

func (a *Agent) logf(format string, args ...any) {
	if a.Log != nil {
		fmt.Fprintf(a.Log, format+"\n", args...)
	}
}

func toMap(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{"type": "object"}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	return m, err
}

func compact(m map[string]any) string {
	b, _ := json.Marshal(redactArgs(m))
	b = bytes.ReplaceAll(b, []byte(`\n`), []byte(` `))
	if len(b) > 160 {
		return string(b[:157]) + "..."
	}
	return string(b)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// effort normalizes a routine's effort: "default" means none.
func effort(e string) string {
	if e == "default" {
		return ""
	}
	return e
}

// checkOutput validates a structured result against the routine's schema.
func checkOutput(schema map[string]any, v any) error {
	if v == nil {
		return errors.New("the routine declares an output schema, but the result came back without one")
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	// Validate the JSON form (numbers as float64, objects as maps).
	var instance any
	if b, err = json.Marshal(v); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &instance); err != nil {
		return err
	}
	if err := resolved.Validate(instance); err != nil {
		return fmt.Errorf("the result doesn't match the routine's output schema: %w", err)
	}
	return nil
}
