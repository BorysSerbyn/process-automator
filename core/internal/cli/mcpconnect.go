package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/claudemcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// MCPConnectOutput is `mcp connect --json`: Claude Code has the app's MCP
// server at user scope (in every folder, listed by /mcp).
type MCPConnectOutput struct {
	// Server is its name in Claude Code (the app's command).
	Server string `json:"server"`
	// Command is what Claude Code runs to start it.
	Command []string `json:"command"`
	// Already is true when it was registered already, and nothing changed.
	Already bool `json:"already,omitempty"`
}

// runClaudeMCP runs the claude command line argv gives for the installed
// Claude Code's version. Tests replace it: they never run the real claude.
var runClaudeMCP = func(ctx context.Context, argv func(agent.ClaudeCodeProtocol) []string) error {
	path, err := agent.FindClaude()
	if err != nil {
		return err
	}
	protocol, err := agent.ClaudeCodeAt(ctx, path)
	if err != nil {
		return err
	}
	args := argv(protocol)
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude %s: %v: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// mcpServeCommand is what Claude Code runs to start the app's MCP server:
// the CLI by a path that survives updates (the link the installer puts in
// ~/.local/bin, else the command on PATH, else this binary).
func mcpServeCommand() ([]string, error) {
	cli := ""
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".local", "bin", product.Command); isFile(p) {
			cli = p
		}
	}
	if cli == "" {
		if p, err := exec.LookPath(product.Command); err == nil {
			cli, _ = filepath.Abs(p)
		}
	}
	if cli == "" {
		p, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cli = p
	}
	return []string{cli, "mcp", "serve"}, nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func newMCPConnectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Register this app's MCP server with Claude Code, at user scope (every folder)",
		Long: "Runs `claude mcp add --scope user " + product.Command + " -- <" + product.Command + "> mcp serve`, so " +
			"Claude Code lists the app's tools (/mcp) in every folder, by a path that survives updates. Nothing " +
			"changes when it's registered already; one whose command is gone is replaced. Setup does it on the " +
			"claude-code backend.",
		Annotations: map[string]string{annNotReadOnly: "it changes Claude Code's own config"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			command, err := mcpServeCommand()
			if err != nil {
				return err
			}
			out := MCPConnectOutput{Server: product.Command, Command: command}
			problem, err := claudemcp.Problem(product.Command)
			if err != nil {
				return err
			}
			if problem == "" {
				out.Already = true
				return emit(cmd, out, "Claude Code already has "+product.Command+"'s MCP server.")
			}
			path, _ := claudemcp.ConfigPath()
			if _, there, _ := claudemcp.UserServer(path, product.Command); there {
				remove := func(p agent.ClaudeCodeProtocol) []string { return p.MCPRemoveArgs(product.Command) }
				if err := runClaudeMCP(cmd.Context(), remove); err != nil {
					return err
				}
			}
			add := func(p agent.ClaudeCodeProtocol) []string { return p.MCPAddArgs(product.Command, command) }
			if err := runClaudeMCP(cmd.Context(), add); err != nil {
				return err
			}
			if problem, err := claudemcp.Problem(product.Command); err != nil {
				return err
			} else if problem != "" {
				return errors.New("claude mcp add didn't register it: " + problem)
			}
			return emit(cmd, out, "Connected: Claude Code runs `"+strings.Join(command, " ")+
				"` for its "+product.Command+" MCP server (/mcp lists it; a running Claude Code session needs a restart).")
		},
	}
	jsonFlag(cmd.Flags(), MCPConnectOutput{})
	return cmd
}
