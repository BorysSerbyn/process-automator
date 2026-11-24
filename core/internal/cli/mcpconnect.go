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
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
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

// runClaude runs the claude at path with args. Tests replace it: they never
// run the real claude.
var runClaude = func(ctx context.Context, path string, args ...string) error {
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
			app, err := loadApp(cmd)
			if err != nil {
				return err
			}
			path, protocol, err := engine.FindClaudeCode(app)
			if err != nil {
				return err
			}
			out := MCPConnectOutput{Server: product.Command, Command: command}
			problem, err := agent.ClaudeMCPProblem(protocol, product.Command)
			if err != nil {
				return err
			}
			if problem == "" {
				out.Already = true
				return emit(cmd, out, "Claude Code already has "+product.Command+"'s MCP server.")
			}
			if _, there, _ := protocol.MCPServer(product.Command); there {
				if err := runClaude(cmd.Context(), path, protocol.MCPRemoveArgs(product.Command)...); err != nil {
					return err
				}
			}
			if err := runClaude(cmd.Context(), path, protocol.MCPAddArgs(product.Command, command)...); err != nil {
				return err
			}
			if problem, err := agent.ClaudeMCPProblem(protocol, product.Command); err != nil {
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
