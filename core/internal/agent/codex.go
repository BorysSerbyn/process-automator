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
	"path/filepath"
	"slices"
	"strings"
)

// Codex is OpenAI's Codex CLI (`codex exec`) as a CLI tool, so routines run
// on the user's own Codex sign-in (`codex login`: a ChatGPT plan or an API key).
//
// It's isolated from the user's own setup as far as Codex allows: their
// config.toml isn't loaded (--ignore-user-config: no MCP servers, profiles,
// model or provider settings, hooks; the sign-in still comes from CODEX_HOME),
// nor their execpolicy rules (--ignore-rules), and the folder's own Codex
// config stays off (Codex loads a project's config only for a project trusted
// in the user's config, which isn't read). Codex's own AGENTS.md loading is
// off: the folder's AGENTS.md is added to the instructions by the core, as
// Claude Code's CLAUDE.md is. Codex's global AGENTS.md (in CODEX_HOME) still
// loads: nothing turns it off short of another CODEX_HOME, which would leave
// the sign-in behind.
//
// The routine's MCP server is its only MCP server, with its tools approved up
// front (the gateway decides what runs). Codex's apps, plugins, subagents,
// image generation, browser and computer use are off; its own tools (shell,
// file edits, web search, image viewing) are what builtinTools allows (see
// codexBuiltins), in a sandbox that's read-only unless the routine allows
// write. Nothing ever asks for approval (approval_policy never: what would
// need one is refused). Codex picks a saved session's ID and reports it;
// otherwise nothing of the session is saved (--ephemeral). From the
// frontmatter only `model` applies.
type Codex struct {
	// Path is the codex binary.
	Path string
}

// Name implements CLITool.
func (*Codex) Name() string { return "codex" }

// PicksSessionID implements SessionPicker: Codex names its sessions (threads).
func (*Codex) PicksSessionID() bool { return true }

// FindCodex locates the codex binary on PATH (which includes the user's
// login-shell PATH, whoever started the command: see sysenv).
func FindCodex() (string, error) {
	p, err := exec.LookPath("codex")
	if err != nil {
		return "", errors.New("codex backend: the `codex` CLI isn't on PATH (https://developers.openai.com/codex/cli)")
	}
	return p, nil
}

// CodexTokenEnv is the environment variable the run's MCP bearer token
// reaches Codex in (bearer_token_env_var), so it's never on the command line.
const CodexTokenEnv = "AUTOMATOR_MCP_TOKEN"

// Codex's built-in tool names in builtinTools, and what each allows.
const (
	codexShell     = "shell"      // its shell tool: commands run in the sandbox
	codexWrite     = "write"      // the sandbox may write in the run's folder (commands and file edits)
	codexNetwork   = "network"    // commands in the sandbox may reach the network (with write)
	codexWebSearch = "web_search" // its web search tool
	codexViewImage = "view_image" // looking at image files
)

// CodexBuiltinTools are the names builtinTools takes on the codex backend.
var CodexBuiltinTools = []string{codexShell, codexWrite, codexNetwork, codexWebSearch, codexViewImage}

// codexReadOnlyBuiltins can't change anything (the sandbox stays read-only
// without write): the only ones a dry run gets.
var codexReadOnlyBuiltins = map[string]bool{codexShell: true, codexWebSearch: true, codexViewImage: true}

// codexBuiltins validates the routine's builtinTools for Codex and, in a dry
// run, keeps only those that can't change anything.
func codexBuiltins(names []string, dryRun bool) (map[string]bool, error) {
	allowed := map[string]bool{}
	for _, n := range names {
		if !slices.Contains(CodexBuiltinTools, n) {
			return nil, fmt.Errorf("builtinTools: %q isn't a Codex built-in tool (%s)", n, strings.Join(CodexBuiltinTools, ", "))
		}
		allowed[n] = true
	}
	if allowed[codexNetwork] && !allowed[codexWrite] {
		return nil, errors.New("builtinTools: network needs write too (Codex's read-only sandbox has no network)")
	}
	if dryRun {
		for n := range allowed {
			if !codexReadOnlyBuiltins[n] {
				delete(allowed, n)
			}
		}
	}
	return allowed, nil
}

// CheckCodexBuiltins says whether builtinTools are names the codex backend takes.
func CheckCodexBuiltins(names []string) error {
	_, err := codexBuiltins(names, false)
	return err
}

// CodexModel maps a Genkit model name ("openai/gpt-5.5") to a Codex model ID.
// Empty means Codex's default.
func CodexModel(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	provider, id, ok := strings.Cut(name, "/")
	if !ok {
		return name, nil
	}
	if provider != "openai" {
		return "", fmt.Errorf("the codex backend runs only OpenAI models (openai/<model>), not %q", name)
	}
	return id, nil
}

// codexEfforts are the routine efforts Codex takes as model_reasoning_effort,
// under the same names.
var codexEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// codexEffort maps a routine's effort to Codex's model_reasoning_effort ("":
// the model's default).
func codexEffort(e string) (string, error) {
	if e == "" || slices.Contains(codexEfforts, e) {
		return e, nil
	}
	return "", fmt.Errorf("effort %q isn't one Codex takes (%s)", e, strings.Join(codexEfforts, ", "))
}

// Command implements CLITool.
func (b *Codex) Command(ctx context.Context, r CLIRun) (*exec.Cmd, error) {
	model, err := CodexModel(r.Model)
	if err != nil {
		return nil, err
	}
	effort, err := codexEffort(r.Effort)
	if err != nil {
		return nil, err
	}
	builtins, err := codexBuiltins(r.BuiltinTools, r.DryRun)
	if err != nil {
		return nil, err
	}
	instructions, err := withProjectInstructions(r.System, r.Folder, "AGENTS.md")
	if err != nil {
		return nil, err
	}
	sandbox := "read-only"
	if builtins[codexWrite] {
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
	if model != "" {
		args = append(args, "--model", model)
	}
	if r.OutputSchema != nil {
		path, err := writeCodexSchema(r.TempDir, r.OutputSchema)
		if err != nil {
			return nil, err
		}
		args = append(args, "--output-schema", path)
	}
	webSearch := "disabled"
	if builtins[codexWebSearch] {
		webSearch = "live"
	}
	mcp := "mcp_servers." + r.MCP.Name + "."
	config := []string{
		`approval_policy="never"`, // unattended: what would need an approval is refused
		"developer_instructions=" + tomlString(instructions),
		"project_doc_max_bytes=0", // the folder's AGENTS.md comes with the instructions instead
		`history.persistence="none"`,
		"check_for_update_on_startup=false",
		"analytics.enabled=false", // my own choice is in the config that isn't read
		"web_search=" + tomlString(webSearch),
		"features.shell_tool=" + fmt.Sprint(builtins[codexShell]),
		"features.view_image=" + fmt.Sprint(builtins[codexViewImage]),
		// No tools but the routine's MCP server and the built-ins it allows.
		"features.apps=false", "features.plugins=false", "features.tool_suggest=false",
		"features.multi_agent=false", "features.image_generation=false", "features.browser_use=false",
		"features.computer_use=false", "features.in_app_browser=false", "features.memories=false",
		"features.goals=false", "features.hooks=false",
		"skills.include_instructions=false", "skills.bundled.enabled=false",
		"tools.experimental_request_user_input.enabled=false", // nobody answers
		mcp + "url=" + tomlString(r.MCP.URL),
		mcp + "bearer_token_env_var=" + tomlString(CodexTokenEnv),
		mcp + "required=true",                         // a run that can't reach its tools fails
		mcp + `default_tools_approval_mode="approve"`, // the gateway decides what runs
		mcp + `omit_tools_from=["deferred"]`,          // in the model's tool list, not behind tool_search
		mcp + "tool_timeout_sec=3600.0",               // the routine's timeout bounds a run
	}
	if builtins[codexNetwork] {
		config = append(config, "sandbox_workspace_write.network_access=true")
	}
	if effort != "" {
		config = append(config, "model_reasoning_effort="+tomlString(effort))
	}
	for _, kv := range config {
		args = append(args, "--config", kv)
	}
	args = append(args, "-") // the prompt comes on stdin
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Env = codexEnv(r.MCP.Token)
	return cmd, nil
}

// codexEnv is the caller's environment with the MCP token, minus API keys
// that would make Codex bill them instead of its own sign-in.
func codexEnv(token string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "CODEX_API_KEY", "OPENAI_API_KEY", CodexTokenEnv:
			continue
		}
		env = append(env, kv)
	}
	return append(env, CodexTokenEnv+"="+token)
}

// writeCodexSchema writes the output schema, in the strict form Codex asks
// the model for, to a file in dir.
func writeCodexSchema(dir string, schema map[string]any) (string, error) {
	if dir == "" {
		return "", errors.New("codex: no folder for the output schema")
	}
	b, err := json.Marshal(strictSchema(schema))
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "output-schema.json")
	return path, os.WriteFile(path, b, 0o600)
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

// Result implements CLITool: the events of `codex exec --json`. The run
// succeeded when its turn completed with a final message.
func (b *Codex) Result(r CLIRun, stdout, stderr []byte, runErr error) (*Output, error) {
	out := &Output{}
	var completed bool
	var final, failed, lastErr string
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
				final = e.Item.Text
			}
		case "turn.completed":
			completed = true
			if u := e.Usage; u != nil {
				out.InputTokens += u.InputTokens
				out.CachedTokens += u.CachedInputTokens
				out.OutputTokens += u.OutputTokens
			}
		case "turn.failed":
			if e.Error != nil {
				failed = e.Error.Message
			}
		case "error":
			lastErr = e.Message // may have been retried: fatal only if the turn didn't complete
		}
	}
	out.Text = final
	if !r.SaveSession {
		out.SessionID = ""
	}
	if runErr == nil && failed == "" && completed && strings.TrimSpace(final) != "" {
		if r.OutputSchema != nil {
			var v any
			if err := json.Unmarshal([]byte(final), &v); err != nil {
				return out, fmt.Errorf("codex: the result isn't the JSON the routine's output schema asks for: %w", err)
			}
			out.Structured = dropOptionalNulls(r.OutputSchema, v)
		}
		return out, nil
	}
	msg := failed
	if msg == "" {
		msg = lastErr
	}
	if msg == "" {
		msg = stderrError(string(stderr))
	}
	switch {
	case msg == "" && runErr != nil:
		msg = runErr.Error()
	case msg == "" && completed:
		msg = "the run ended without a final message"
	case msg == "":
		msg = "the run ended before its turn completed"
	}
	return out, codexError(msg)
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

// CodexLoginStatus runs `codex login status`, which reads the sign-in Codex
// has stored (no network): "" when signed in, else why not. Runs read it from
// CODEX_HOME's auth.json (Codex's default store; they don't load the user's
// config, where another store would be chosen), so it's checked there too.
func CodexLoginStatus(ctx context.Context, path string) (problem string, err error) {
	cmd := exec.CommandContext(ctx, path, "--config", `cli_auth_credentials_store="file"`, "login", "status")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "", nil
	case errors.As(err, &exit):
		text := strings.TrimSpace(out.String())
		if text == "" {
			text = "not signed in"
		}
		return text, nil
	}
	return "", err
}
