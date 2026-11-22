// Package claudemcp reads which MCP servers Claude Code has at user scope
// (what `claude mcp add --scope user` registers), from Claude Code's own
// config file: no claude process, so it's quick enough for every scheduler
// pass. Registering goes through the claude CLI (see cli's `mcp connect`).
package claudemcp

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ConfigPath is Claude Code's config file, which holds its user-scope MCP
// servers: .claude.json in $CLAUDE_CONFIG_DIR, or in my home folder.
func ConfigPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

// Server is a stdio MCP server as Claude Code runs it.
type Server struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// UserServer is the user-scope server registered as name; ok is false when
// there's none (or no config file yet).
func UserServer(configPath, name string) (s Server, ok bool, err error) {
	b, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return Server{}, false, nil
	}
	if err != nil {
		return Server{}, false, err
	}
	var cfg struct {
		MCPServers map[string]Server `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Server{}, false, err
	}
	s, ok = cfg.MCPServers[name]
	return s, ok, nil
}

// Problem says why Claude Code doesn't have the app's MCP server (registered
// as name) at user scope, "" when it has: none is registered, or one whose
// command is gone.
func Problem(name string) (string, error) {
	path, err := ConfigPath()
	if err != nil {
		return "", err
	}
	s, ok, err := UserServer(path, name)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "Claude Code doesn't have " + name + "'s MCP server, so /mcp doesn't list it", nil
	case s.Command != "" && filepath.IsAbs(s.Command) && !isFile(s.Command):
		return "Claude Code's " + name + " MCP server runs " + s.Command + ", which isn't there anymore", nil
	}
	return "", nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
