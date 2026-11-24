package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// codex0_171_0 is Codex from 0.171.0, the version the backend was built and
// recorded against (testdata/codex/recorded-0.171.0.jsonl).
type codex0_171_0 struct{}

func (codex0_171_0) Since() Version { return mustVersion("0.171.0") }

// codexTokenEnv is the environment variable the run's MCP bearer token
// reaches Codex in (bearer_token_env_var), so it's never on the command line.
const codexTokenEnv = "AUTOMATOR_MCP_TOKEN"

func (codex0_171_0) ExecArgs(r CodexRun) []string {
	sandbox := "read-only"
	if r.Builtins[codexWrite] {
		sandbox = "workspace-write"
	}
	args := []string{
		"exec", "--json",
		"--ignore-user-config", "--ignore-rules", // none of my Codex config, profiles or rules
		"--skip-git-repo-check", // routine folders needn't be git repositories
		"--sandbox", sandbox,
	}
	if !r.SaveSession {
		args = append(args, "--ephemeral")
	}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if r.SchemaFile != "" {
		args = append(args, "--output-schema", r.SchemaFile)
	}
	webSearch := "disabled"
	if r.Builtins[codexWebSearch] {
		webSearch = "live"
	}
	mcp := "mcp_servers." + r.MCP.Name + "."
	config := []string{
		`approval_policy="never"`, // unattended: what would need an approval is refused
		"developer_instructions=" + tomlString(r.Instructions),
		"project_doc_max_bytes=0", // the folder's AGENTS.md comes with the instructions instead
		`history.persistence="none"`,
		"check_for_update_on_startup=false",
		"analytics.enabled=false", // my own choice is in the config that isn't read
		"web_search=" + tomlString(webSearch),
		"features.shell_tool=" + fmt.Sprint(r.Builtins[codexShell]),
		"features.view_image=" + fmt.Sprint(r.Builtins[codexViewImage]),
		// No tools but the routine's MCP server and the built-ins it allows.
		"features.apps=false", "features.plugins=false", "features.tool_suggest=false",
		"features.multi_agent=false", "features.image_generation=false", "features.browser_use=false",
		"features.computer_use=false", "features.in_app_browser=false", "features.memories=false",
		"features.goals=false", "features.hooks=false",
		"skills.include_instructions=false", "skills.bundled.enabled=false",
		"tools.experimental_request_user_input.enabled=false", // nobody answers
		mcp + "url=" + tomlString(r.MCP.URL),
		mcp + "bearer_token_env_var=" + tomlString(codexTokenEnv),
		mcp + "required=true",                                               // a run that can't reach its tools fails
		mcp + `default_tools_approval_mode="approve"`,                       // the gateway decides what runs
		mcp + `omit_tools_from=["deferred"]`,                                // in the model's tool list, not behind tool_search
		mcp + fmt.Sprintf("tool_timeout_sec=%.1f", r.ToolTimeout.Seconds()), // the routine's timeout bounds a run
	}
	if r.Builtins[codexNetwork] {
		config = append(config, "sandbox_workspace_write.network_access=true")
	}
	if r.Effort != "" {
		config = append(config, "model_reasoning_effort="+tomlString(r.Effort))
	}
	for _, kv := range config {
		args = append(args, "--config", kv)
	}
	return append(args, "-") // the prompt comes on stdin
}

// ExecEnv strips the API keys that would make Codex bill them instead of its
// own sign-in.
func (codex0_171_0) ExecEnv(base []string, mcpToken string) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "CODEX_API_KEY", "OPENAI_API_KEY", codexTokenEnv:
			continue
		}
		env = append(env, kv)
	}
	return append(env, codexTokenEnv+"="+mcpToken)
}

// codexEfforts are the routine efforts Codex takes as model_reasoning_effort,
// under the same names.
var codexEfforts = []string{"low", "medium", "high", "xhigh", "max"}

func (codex0_171_0) Efforts() []string { return codexEfforts }

// Codex's built-in tool names in builtinTools. Each is named by what it
// allows, since Codex's own switches (a sandbox mode, feature flags) aren't
// tools.
const (
	codexShell     = "shell"
	codexWrite     = "write"
	codexNetwork   = "network"
	codexWebSearch = "web_search"
	codexViewImage = "view_image"
)

// codexBuiltins0_171_0 are the names builtinTools takes on Codex.
var codexBuiltins0_171_0 = BuiltinSet{Tools: []BuiltinTool{
	{Name: codexShell, Allows: "running commands, in Codex's sandbox (read-only without write)", ReadOnly: true},
	{Name: codexWrite, Allows: "commands and Codex's file edits changing files in the run's folder"},
	{Name: codexNetwork, Allows: "commands reaching the network (with write)"},
	{Name: codexWebSearch, Allows: "searching the web", ReadOnly: true},
	{Name: codexViewImage, Allows: "looking at image files", ReadOnly: true},
}}

func (codex0_171_0) Builtins() *BuiltinSet { return &codexBuiltins0_171_0 }

func (codex0_171_0) AllowBuiltins(names []string, dryRun bool) (map[string]bool, error) {
	return codexBuiltins(&codexBuiltins0_171_0, names, dryRun)
}

// codexBuiltins validates builtinTools against set (an implementation's
// Builtins: one that changes them overrides AllowBuiltins too, with its own)
// and, in a dry run, keeps only those that can't change anything.
func codexBuiltins(set *BuiltinSet, names []string, dryRun bool) (map[string]bool, error) {
	allowed := map[string]bool{}
	for _, n := range names {
		if !set.known(n) {
			return nil, fmt.Errorf("builtinTools: %q isn't a Codex built-in tool (%s)", n, strings.Join(set.Names(), ", "))
		}
		allowed[n] = true
	}
	if allowed[codexNetwork] && !allowed[codexWrite] {
		return nil, errors.New("builtinTools: network needs write too (Codex's read-only sandbox has no network)")
	}
	if dryRun {
		for n := range allowed {
			if !set.readOnly(n) {
				delete(allowed, n)
			}
		}
	}
	return allowed, nil
}

func (codex0_171_0) ProjectInstructions() string { return "AGENTS.md" }

// OutputSchema: OpenAI's strict structured outputs (strictSchema).
func (codex0_171_0) OutputSchema(schema map[string]any) map[string]any { return strictSchema(schema) }

func (codex0_171_0) Structured(schema map[string]any, final string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(final), &v); err != nil {
		return nil, err
	}
	return dropOptionalNulls(schema, v), nil
}

// Events reads `codex exec --json`'s events (codexEvent).
func (codex0_171_0) Events(stdout []byte) CodexEvents {
	var out CodexEvents
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var e codexEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue // not an event
		}
		switch e.Type {
		case "thread.started":
			out.SessionID = e.ThreadID
		case "item.completed":
			if e.Item != nil && e.Item.Type == "agent_message" {
				out.Final = e.Item.Text
			}
		case "turn.completed":
			out.Completed = true
			if u := e.Usage; u != nil {
				out.InputTokens += u.InputTokens
				out.CachedTokens += u.CachedInputTokens
				out.OutputTokens += u.OutputTokens
			}
		case "turn.failed":
			if e.Error != nil {
				out.Failed = e.Error.Message
			}
		case "error":
			out.LastError = e.Message // may have been retried: fatal only if the turn didn't complete
		}
	}
	return out
}

func (codex0_171_0) StderrError(stderr string) string { return stderrError(stderr) }

func (codex0_171_0) RunError(msg string) error { return codexError(msg) }

func (codex0_171_0) LoginCommand() string { return "codex login" }

// LoginStatusArgs: `codex login status` reads the sign-in Codex has stored
// (no network). Runs read it from CODEX_HOME's auth.json (Codex's default
// store; they don't load the user's config, where another store would be
// chosen), so it's checked there too.
func (codex0_171_0) LoginStatusArgs() []string {
	return []string{"--config", `cli_auth_credentials_store="file"`, "login", "status"}
}

// LoginProblem: signed in exits 0; otherwise what it printed says why.
func (codex0_171_0) LoginProblem(output string, exitedZero bool) string {
	if exitedZero {
		return ""
	}
	if text := strings.TrimSpace(output); text != "" {
		return text
	}
	return "not signed in"
}

// codexEvent is one line of `codex exec --json` (its ThreadEvent).
type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"` // thread.started
	Message  string `json:"message"`   // error
	Item     *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"` // item.*
	Error *struct {
		Message string `json:"message"`
	} `json:"error"` // turn.failed
	Usage *struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
	} `json:"usage"` // turn.completed
}

// codexError words a failed run's message: a usage limit or a missing
// sign-in says so plainly.
func codexError(msg string) error {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "required mcp servers failed to initialize"):
		// Before "401": the app's own MCP server refusing isn't a Codex sign-in.
		return fmt.Errorf("codex: the routine's tools couldn't be reached: %s", msg)
	case strings.Contains(low, "usage limit") || strings.Contains(low, "quota exceeded"):
		return fmt.Errorf("codex: Codex's usage limit is reached: %s", msg)
	case strings.Contains(low, "not logged in") || strings.Contains(low, "401") || strings.Contains(low, "unauthorized"):
		return fmt.Errorf("codex: %s (sign in by running `codex login`)", msg)
	}
	return fmt.Errorf("codex: %s", msg)
}

// stderrError is the error Codex printed last on stderr ("Error: …", which
// can run to several kilobytes of nested causes), shortened from its end;
// without one, stderr's last lines.
func stderrError(stderr string) string {
	s := strings.TrimSpace(stderr)
	if i := strings.LastIndex(s, "Error: "); i >= 0 && (i == 0 || s[i-1] == '\n') {
		s = s[i+len("Error: "):]
	} else if len(s) > 2000 {
		s = "…" + s[len(s)-2000:]
	}
	if len(s) > 600 {
		s = s[:600] + "…"
	}
	return s
}

// strictSchema is a copy of a JSON schema in the form OpenAI's strict
// structured outputs take (Codex asks for one): every object lists all its
// properties as required and allows no others, and a property that wasn't
// required may be null instead (see dropOptionalNulls).
func strictSchema(s map[string]any) map[string]any {
	if alts, ok := s["anyOf"].([]any); ok && allTyped(alts) {
		// Typed alternatives are the whole schema; Picoschema's optional
		// fields repeat the first one's keywords beside them, which strict
		// mode doesn't take.
		s = map[string]any{"anyOf": alts, "description": s["description"]}
		if s["description"] == nil {
			delete(s, "description")
		}
	}
	out := make(map[string]any, len(s)+2)
	for k, v := range s {
		out[k] = v
	}
	for _, k := range []string{"items", "not"} {
		if sub, ok := s[k].(map[string]any); ok {
			out[k] = strictSchema(sub)
		}
	}
	for _, k := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if list, ok := s[k].([]any); ok {
			subs := make([]any, len(list))
			for i, e := range list {
				subs[i] = e
				if sub, ok := e.(map[string]any); ok {
					subs[i] = strictSchema(sub)
				}
			}
			out[k] = subs
		}
	}
	for _, k := range []string{"$defs", "definitions"} {
		if defs, ok := s[k].(map[string]any); ok {
			subs := make(map[string]any, len(defs))
			for name, e := range defs {
				subs[name] = e
				if sub, ok := e.(map[string]any); ok {
					subs[name] = strictSchema(sub)
				}
			}
			out[k] = subs
		}
	}
	props, ok := s["properties"].(map[string]any)
	if !ok {
		return out
	}
	required := map[string]bool{}
	for _, r := range stringList(s["required"]) {
		required[r] = true
	}
	strict := make(map[string]any, len(props))
	names := make([]string, 0, len(props))
	for name, p := range props {
		names = append(names, name)
		strict[name] = p
		if pm, ok := p.(map[string]any); ok {
			pm = strictSchema(pm)
			strict[name] = pm
			if !required[name] && !nullable(pm) {
				strict[name] = map[string]any{"anyOf": []any{pm, map[string]any{"type": "null"}}}
			}
		}
	}
	slices.Sort(names)
	out["properties"], out["required"] = strict, names
	if _, set := out["additionalProperties"]; !set {
		out["additionalProperties"] = false
	}
	return out
}

// nullable says whether a schema already takes null at its top level (its
// type, or one of its anyOf alternatives).
func nullable(s map[string]any) bool {
	switch t := s["type"].(type) {
	case string:
		return t == "null"
	case []any:
		return slices.Contains(t, any("null"))
	}
	alts, _ := s["anyOf"].([]any)
	return slices.ContainsFunc(alts, func(a any) bool {
		m, ok := a.(map[string]any)
		return ok && nullable(m)
	})
}

// allTyped says whether every alternative is a schema with a type.
func allTyped(alts []any) bool {
	for _, a := range alts {
		if m, ok := a.(map[string]any); !ok || m["type"] == nil {
			return false
		}
	}
	return len(alts) > 0
}

func stringList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []string:
		out = t
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// dropOptionalNulls removes the nulls strictSchema let the model put in
// properties that weren't required, so the result matches the routine's own
// schema (where they're simply absent).
func dropOptionalNulls(schema map[string]any, v any) any {
	switch t := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		required := map[string]bool{}
		for _, r := range stringList(schema["required"]) {
			required[r] = true
		}
		for k, e := range t {
			ps, _ := props[k].(map[string]any)
			if e == nil && props != nil && !required[k] && (ps == nil || !nullable(ps)) {
				delete(t, k)
				continue
			}
			if ps != nil {
				t[k] = dropOptionalNulls(ps, e)
			}
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for i, e := range t {
			if items != nil {
				t[i] = dropOptionalNulls(items, e)
			}
		}
	}
	return v
}

// tomlString is s as a TOML basic string, for `--config key=value` (Codex
// parses the value as TOML). A JSON string is one, but for DEL, which TOML
// wants escaped too.
func tomlString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.ReplaceAll(strings.TrimSuffix(buf.String(), "\n"), "\x7f", `\u007f`)
}
