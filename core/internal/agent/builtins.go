package agent

import "slices"

// BuiltinTool is a name builtinTools takes on an agent CLI backend.
type BuiltinTool struct {
	Name string
	// Allows says what allowing it lets a run do.
	Allows string
	// ReadOnly tools can't change anything: dry runs keep only these (built-in
	// tools run inside the CLI, where the gateway can't intercept them).
	ReadOnly bool
}

// BuiltinSet is an agent CLI's built-in tool vocabulary: the names a
// routine's builtinTools take on its backend.
type BuiltinSet struct {
	// Tools are the names it takes, with what each allows. When Open, any of
	// the CLI's own tool names goes, and Tools are the common ones.
	Tools []BuiltinTool
	Open  bool
}

// Names are the set's tool names, in order.
func (s BuiltinSet) Names() []string {
	out := make([]string, len(s.Tools))
	for i, t := range s.Tools {
		out[i] = t.Name
	}
	return out
}

func (s BuiltinSet) known(name string) bool { return slices.Contains(s.Names(), name) }

func (s BuiltinSet) readOnly(name string) bool {
	for _, t := range s.Tools {
		if t.Name == name {
			return t.ReadOnly
		}
	}
	return false
}

// ClaudeBuiltins are Claude Code's built-in tools: any of its tool names
// goes; these are the common ones, and the read-only ones are all a dry run
// gets.
var ClaudeBuiltins = BuiltinSet{Open: true, Tools: []BuiltinTool{
	{Name: "Read", Allows: "reading files", ReadOnly: true},
	{Name: "Glob", Allows: "finding files by name pattern", ReadOnly: true},
	{Name: "Grep", Allows: "searching file contents", ReadOnly: true},
	{Name: "WebSearch", Allows: "searching the web", ReadOnly: true},
	{Name: "WebFetch", Allows: "fetching a web page", ReadOnly: true},
	{Name: "Bash", Allows: "running shell commands"},
	{Name: "Edit", Allows: "changing files"},
	{Name: "Write", Allows: "creating and overwriting files"},
}}

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

// CodexBuiltins are the names builtinTools takes on the codex backend.
var CodexBuiltins = BuiltinSet{Tools: []BuiltinTool{
	{Name: codexShell, Allows: "running commands, in Codex's sandbox (read-only without write)", ReadOnly: true},
	{Name: codexWrite, Allows: "commands and Codex's file edits changing files in the run's folder"},
	{Name: codexNetwork, Allows: "commands reaching the network (with write)"},
	{Name: codexWebSearch, Allows: "searching the web", ReadOnly: true},
	{Name: codexViewImage, Allows: "looking at image files", ReadOnly: true},
}}
