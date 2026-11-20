package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// The automator's own MCP server makes the CLI available to MCP clients
// (Claude) and, read-only, to routines. Every CLI command is a tool by
// default, derived from the command tree: arguments and flags become the
// input schema, and a call runs the command (with --json when it has it). The
// MCP server is never edited when the CLI grows; mcpOverrides is the one place
// where a tool differs from its command.

// automatorServerName is the server's name for routines (automator.servers).
const automatorServerName = "automator"

// mcpOverride changes how one command (by path, e.g. "auth login") is served.
type mcpOverride struct {
	// exclude is why the command (and its subcommands) isn't a tool.
	exclude string
	// handle replaces running the command, e.g. to ask me for input through
	// the client instead of a terminal. Its tool takes the positional
	// arguments only (the command's flags are for terminals).
	handle func(ctx context.Context, s *automatorServer, req *sdk.CallToolRequest, t cliTool, args map[string]any) (*sdk.CallToolResult, error)
	// after is added to a successful result.
	after string
}

// mcpOverrides is the one place a tool differs from its command, by command path.
func mcpOverrides() map[string]mcpOverride {
	return map[string]mcpOverride{
		"daemon":     {exclude: "runs forever; it's started at login, not called"},
		"mcp serve":  {exclude: "this server itself"},
		"help":       {exclude: "tool descriptions carry the help"},
		"completion": {exclude: "shell completion scripts"},
		// Secrets and sign-ins go to me through the client (elicitation), never
		// through tool arguments or the model.
		"auth login": {handle: mcpLogin},
		// The approval prompt shows a tool call's arguments, not what the routine
		// can reach: trusting is reviewed in a terminal (or automator-ui).
		"routines trust":   {exclude: "trusting a routine is my decision, after reviewing what it can reach"},
		"uninstall":        {exclude: "removing the app (and this server with it) is mine, from a terminal"},
		"usage statusline": {exclude: "it's Claude Code's status line; it reads Claude Code's input on stdin"},
		"update": {after: "Later calls run the new version, but this server's tool list is from before " +
			"the update: reconnect the " + product.Command + " MCP server to pick up new or changed tools."},
	}
}

func newMCPServeCmd() *cobra.Command {
	var readOnlyMode bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the CLI as MCP tools over stdio, for Claude and other MCP clients",
		Long: "Every " + product.Command + " command is a tool. Read-only commands are marked read-only; the rest are " +
			"marked destructive, so clients ask before calling them. Sign-ins and keys are entered by you in the " +
			"browser (MCP URL elicitation), never passed through the model. --read-only serves only read-only " +
			"tools (what routines get as the automator server).",
		Example: "  claude mcp add " + product.Command + " -- " + product.Command + " mcp serve",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			s := &automatorServer{
				root: cmd.Root(), exe: exe, configPath: a.Layout.Config(),
				plugins: pluginsFrom(cmd.Context()), readOnly: readOnlyMode, base: cmd.Context(),
			}
			srv, err := s.build()
			if err != nil {
				return err
			}
			err = srv.Run(cmd.Context(), &sdk.StdioTransport{})
			s.drain(20 * time.Second)
			if cmd.Context().Err() != nil {
				return nil // stopped by a signal: a normal end
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&readOnlyMode, "read-only", false, "serve only read-only tools")
	return cmd
}

// automatorServer serves a command tree as MCP tools.
type automatorServer struct {
	root       *cobra.Command
	exe        string   // the binary commands run as
	env        []string // extra environment for commands (tests)
	configPath string
	plugins    []pluginapi.Plugin
	// readOnly serves only read-only tools, minus those kept from routines.
	readOnly bool
	logins   loginRegistry
	// base ends with the server (interrupt, terminate): in-flight calls are
	// cancelled with it, and drain waits for them to wind down.
	base     context.Context
	inflight sync.WaitGroup
}

// drain waits (up to limit) for in-flight calls, so their commands record how
// they ended before the server exits.
func (s *automatorServer) drain(limit time.Duration) {
	done := make(chan struct{})
	go func() { s.inflight.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(limit):
	}
}

// cliTool is one command (or read-only variant of one) as a tool.
type cliTool struct {
	tool *sdk.Tool
	cmd  *cobra.Command
	key  string // command path below the root, e.g. "routines list"
	// forced are flags always passed (a read-only variant's flag).
	forced []string
	// args are the positional arguments by property name, in order.
	args []cliArg
	// flags maps property names to flag names.
	flags   map[string]*pflag.Flag
	jsonOut bool
	ro      bool
}

type cliArg struct {
	prop     string
	required bool
}

// tools derives the tools from the command tree.
func (s *automatorServer) tools() []cliTool {
	s.root.InitDefaultHelpCmd()
	s.root.InitDefaultCompletionCmd()
	overrides := mcpOverrides()
	var out []cliTool
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			key := commandKey(sub)
			if sub.Hidden || overrides[key].exclude != "" {
				continue
			}
			if sub.Runnable() {
				out = append(out, s.toolsFor(sub, key, overrides[key])...)
			}
			walk(sub)
		}
	}
	walk(s.root)
	sort.Slice(out, func(i, j int) bool { return out[i].tool.Name < out[j].tool.Name })
	return out
}

func commandKey(c *cobra.Command) string {
	path := strings.Fields(c.CommandPath())
	return strings.Join(path[1:], " ")
}

func toolName(parts ...string) string {
	return strings.ReplaceAll(strings.Join(parts, "_"), "-", "_")
}

// toolsFor is a command's tool, plus a read-only variant when a flag makes it
// read-only (run_dry_run, update_check).
func (s *automatorServer) toolsFor(c *cobra.Command, key string, ov mcpOverride) []cliTool {
	if s.readOnly && c.Annotations[annNotForRoutines] != "" {
		return nil
	}
	roFlag := c.Annotations[annReadOnlyWith]
	noFlags := ov.handle != nil
	var out []cliTool
	if base := s.newTool(c, key, toolName(strings.Fields(key)...), isReadOnly(c), roFlag, "", noFlags); !s.readOnly || base.ro {
		out = append(out, base)
	}
	if roFlag != "" && c.Flags().Lookup(roFlag) != nil {
		out = append(out, s.newTool(c, key, toolName(append(strings.Fields(key), roFlag)...), true, roFlag, roFlag, noFlags))
	}
	return out
}

func (s *automatorServer) newTool(c *cobra.Command, key, name string, ro bool, roFlag, forced string, noFlags bool) cliTool {
	// --json may be inherited from a parent command (settings).
	t := cliTool{cmd: c, key: key, flags: map[string]*pflag.Flag{}, ro: ro, jsonOut: c.Flag("json") != nil}
	props := map[string]any{}
	var required []string
	for _, tok := range strings.Fields(c.Use)[1:] {
		optional := strings.HasPrefix(tok, "[")
		raw := strings.Trim(tok, "[]<>.")
		prop := toolName(strings.Split(raw, "|")[0])
		props[prop] = map[string]any{"type": "string", "description": "argument " + tok}
		t.args = append(t.args, cliArg{prop: prop, required: !optional})
		if !optional {
			required = append(required, prop)
		}
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		switch {
		case noFlags, f.Hidden, f.Name == "help", f.Name == "json", hiddenFromMCP(f):
			return
		case f.Name == roFlag:
			return // the base tool is the real thing; the variant forces the flag
		}
		prop := toolName(f.Name)
		t.flags[prop] = f
		props[prop] = flagSchema(f)
	})
	if forced != "" {
		t.forced = []string{"--" + forced}
	}
	desc := c.Short
	if c.Long != "" {
		desc += "\n\n" + c.Long
	}
	if forced != "" {
		desc = fmt.Sprintf("`%s --%s` (read-only). %s", key, forced, desc)
	}
	if c.Example != "" {
		desc += "\n\nCLI examples:\n" + c.Example
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	ann := &sdk.ToolAnnotations{ReadOnlyHint: ro, OpenWorldHint: ptr(true)}
	if !ro {
		ann.DestructiveHint = ptr(true)
	}
	t.tool = &sdk.Tool{Name: name, Description: desc, InputSchema: schema, Annotations: ann}
	return t
}

func ptr[T any](v T) *T { return &v }

func flagSchema(f *pflag.Flag) map[string]any {
	m := map[string]any{"description": f.Usage}
	switch f.Value.Type() {
	case "bool":
		m["type"] = "boolean"
	case "int", "int64", "int32", "uint":
		m["type"] = "integer"
	case "stringArray", "stringSlice":
		m["type"] = "array"
		m["items"] = map[string]any{"type": "string"}
	default:
		m["type"] = "string"
	}
	if d := f.DefValue; d != "" && d != "[]" && d != "false" {
		m["description"] = fmt.Sprintf("%s (default %s)", f.Usage, d)
	}
	return m
}

// checkArgs enforces the tool's input schema: known properties, right types.
func (t cliTool) checkArgs(args map[string]any) error {
	positional := map[string]bool{}
	for _, a := range t.args {
		positional[a.prop] = true
	}
	for k, v := range args {
		if v == nil {
			continue
		}
		var want string
		if positional[k] {
			want = "string"
		} else if f, ok := t.flags[k]; ok {
			want = flagSchema(f)["type"].(string)
		} else {
			return fmt.Errorf("unknown argument %q", k)
		}
		ok := false
		switch want {
		case "string":
			_, ok = v.(string)
		case "boolean":
			_, ok = v.(bool)
		case "integer":
			n, isNum := v.(float64)
			ok = isNum && n == float64(int64(n))
		case "array":
			items, isList := v.([]any)
			ok = isList
			for _, e := range items {
				if _, isStr := e.(string); !isStr {
					ok = false
				}
			}
		}
		if !ok {
			kinds := map[string]string{"string": "a string", "boolean": "true or false", "integer": "a whole number", "array": "a list of strings"}
			return fmt.Errorf("argument %q must be %s", k, kinds[want])
		}
	}
	return nil
}

// argv is the command line for a call: path, flags, then positional args, and
// what the command reads on stdin (a flag sent that way, e.g. a routine's
// source, which can be too big for a command line).
func (t cliTool) argv(configPath string, args map[string]any) (argv []string, stdin string, err error) {
	out := append([]string{"--config", configPath}, strings.Fields(t.key)...)
	out = append(out, t.forced...)
	if t.jsonOut {
		out = append(out, "--json")
	}
	props := make([]string, 0, len(t.flags))
	for p := range t.flags {
		props = append(props, p)
	}
	sort.Strings(props)
	for _, p := range props {
		v, ok := args[p]
		if !ok || v == nil {
			continue
		}
		name := "--" + t.flags[p].Name
		if via := t.flags[p].Annotations[annViaStdin]; len(via) > 0 {
			stdin = fmt.Sprint(v)
			out = append(out, "--"+via[0]+"=-")
			continue
		}
		switch v := v.(type) {
		case bool:
			out = append(out, fmt.Sprintf("%s=%v", name, v))
		case []any:
			for _, e := range v {
				out = append(out, fmt.Sprintf("%s=%v", name, e))
			}
		case float64:
			out = append(out, fmt.Sprintf("%s=%d", name, int64(v)))
		default:
			out = append(out, fmt.Sprintf("%s=%v", name, v))
		}
	}
	out = append(out, "--")
	for _, a := range t.args {
		v, ok := args[a.prop]
		if !ok || v == nil || v == "" {
			if a.required {
				return nil, "", fmt.Errorf("%s is required", a.prop)
			}
			break
		}
		out = append(out, fmt.Sprint(v))
	}
	return out, stdin, nil
}

// build makes the MCP server.
func (s *automatorServer) build() (*sdk.Server, error) {
	srv := sdk.NewServer(&sdk.Implementation{Name: product.Command, Version: product.Version}, &sdk.ServerOptions{
		Instructions: product.Command + " runs scheduled LLM routines (Dotprompt .prompt files) with tools from MCP servers " +
			"(plugins). These tools are its CLI: read routines, runs, settings and plugins; create or change " +
			"routines (they start disabled: dry-run them, then enable with settings_set routines.<name>.enabled true); " +
			"add plugins; sign in (auth_login asks the user in their browser; never ask the user to paste a secret " +
			"into the chat). Routine files use dotted frontmatter keys (automator.servers, automator.schedule).",
	})
	overrides := mcpOverrides()
	for _, t := range s.tools() {
		t := t
		ov := overrides[t.key]
		srv.AddTool(t.tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			s.inflight.Add(1)
			defer s.inflight.Done()
			if s.base != nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				defer context.AfterFunc(s.base, cancel)()
			}
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return toolText("invalid arguments: "+err.Error(), true), nil
				}
			}
			if err := t.checkArgs(args); err != nil {
				return toolText(err.Error(), true), nil
			}
			var res *sdk.CallToolResult
			if ov.handle != nil {
				var err error
				if res, err = ov.handle(ctx, s, req, t, args); err != nil {
					res = toolText(err.Error(), true)
				}
			} else {
				res = s.run(ctx, req, t, args)
			}
			if !res.IsError && ov.after != "" && len(t.forced) == 0 { // not on read-only variants
				res.Content = append(res.Content, &sdk.TextContent{Text: ov.after})
			}
			if !t.ro && res.InputRequests == nil { // an input request isn't the call's outcome
				s.logChange(t, args, res)
			}
			return res, nil
		})
	}
	return srv, nil
}

// run runs the command as a subprocess (so its output can never mix with the
// protocol on stdout), streaming its log lines as progress.
func (s *automatorServer) run(ctx context.Context, req *sdk.CallToolRequest, t cliTool, args map[string]any) *sdk.CallToolResult {
	argv, stdin, err := t.argv(s.configPath, args)
	if err != nil {
		return toolText(err.Error(), true)
	}
	cmd := exec.CommandContext(ctx, s.exe, argv...)
	cmd.Env = append(os.Environ(), s.env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// A cancelled call interrupts the command rather than killing it, so it can
	// record its outcome (a run marks itself failed) and stop what it started.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 15 * time.Second
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return toolText(err.Error(), true)
	}
	if err := cmd.Start(); err != nil {
		return toolText(err.Error(), true)
	}
	var log strings.Builder
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	token := req.Params.GetProgressToken()
	n := 0
	for sc.Scan() {
		line := sc.Text()
		log.WriteString(line + "\n")
		if token != nil {
			n++
			_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: token, Progress: float64(n), Message: line})
		}
	}
	if err := sc.Err(); err != nil {
		// Keep reading, or a command blocked writing to a full pipe never exits.
		_, _ = io.Copy(io.Discard, stderr)
		log.WriteString("(log cut: " + err.Error() + ")\n")
	}
	runErr := cmd.Wait()
	res := &sdk.CallToolResult{IsError: runErr != nil}
	if out := strings.TrimSpace(stdout.String()); out != "" {
		res.Content = append(res.Content, &sdk.TextContent{Text: out})
	}
	if l := strings.TrimSpace(log.String()); l != "" {
		res.Content = append(res.Content, &sdk.TextContent{Text: "[log]\n" + l})
	}
	if runErr != nil && len(res.Content) == 0 {
		res.Content = []sdk.Content{&sdk.TextContent{Text: runErr.Error()}}
	}
	if len(res.Content) == 0 {
		res.Content = []sdk.Content{&sdk.TextContent{Text: "done"}}
	}
	return res
}

func toolText(text string, isError bool) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, IsError: isError}
}

// mcpChange is one line of the MCP change log.
type mcpChange struct {
	At    time.Time      `json:"at"`
	Tool  string         `json:"tool"`
	Args  map[string]any `json:"args,omitempty"`
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
}

var changeLogMu sync.Mutex

// Change log limits: long values are shortened, and the file is rotated
// (one older file kept) past changeLogMax.
const (
	changeLogValueMax = 1000
	changeLogMax      = 5 << 20
)

// logChange records a side-effecting call, so I can see what a client did.
// Secrets go through elicitation, never arguments; values of flags marked
// redacted (a plugin's env and headers, which may hold literal values) are
// left out.
func (s *automatorServer) logChange(t cliTool, args map[string]any, res *sdk.CallToolResult) {
	logged := map[string]any{}
	for k, v := range args {
		if f, ok := t.flags[k]; ok && len(f.Annotations[annRedact]) > 0 {
			v = redactPairs(v)
		}
		if str, ok := v.(string); ok && len(str) > changeLogValueMax {
			v = fmt.Sprintf("%s… (%d bytes)", str[:200], len(str))
		}
		logged[k] = v
	}
	c := mcpChange{At: time.Now(), Tool: t.tool.Name, Args: logged, OK: !res.IsError}
	if res.IsError {
		c.Error = pluginapi.ResultText(res)
		if len(c.Error) > 500 {
			c.Error = c.Error[:500] + "…"
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	changeLogMu.Lock()
	defer changeLogMu.Unlock()
	path := engine.LayoutOf(s.configPath).MCPChanges()
	if fi, err := os.Stat(path); err == nil && fi.Size() > changeLogMax {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: can't record MCP change:", err)
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// automatorPlugin is the core's own server for routines: the read-only tools.
func automatorPlugin(l engine.Layout, plugins []pluginapi.Plugin) pluginapi.Plugin {
	return pluginapi.Plugin{
		Name: automatorServerName,
		Description: "This automator: its routines, runs, settings, templates and plugins (read-only; " +
			"MCP clients get everything with `" + product.Command + " mcp serve`)",
		Serve: func(pluginapi.Host) (*sdk.Server, error) {
			exe, err := os.Executable()
			if err != nil {
				return nil, err
			}
			s := &automatorServer{root: NewRoot(plugins), exe: exe, configPath: l.Config(), plugins: plugins, readOnly: true}
			return s.build()
		},
	}
}

// redactPairs keeps the names of name=value pairs and drops the values.
func redactPairs(v any) any {
	items, ok := v.([]any)
	if !ok {
		return "(redacted)"
	}
	out := make([]any, len(items))
	for i, e := range items {
		name, _, _ := strings.Cut(fmt.Sprint(e), "=")
		out[i] = name + "=(redacted)"
	}
	return out
}
