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

// MCPConnectOutput is `mcp connect --json`: the agent CLIs installed
// (Claude Code at user scope, Codex in its own config) have the app's MCP
// server, in every folder.
type MCPConnectOutput struct {
	// Server is its name in them (the app's command).
	Server string `json:"server"`
	// Command is what they run to start it.
	Command []string `json:"command"`
	// Already is true when every one had it already, and nothing changed.
	Already bool `json:"already,omitempty"`
	// Clients are the CLIs it's in: claude-code, codex.
	Clients []MCPClient `json:"clients"`
}

// MCPClient is one agent CLI that has the app's MCP server.
type MCPClient struct {
	Name string `json:"name" jsonschema:"enum=claude-code,enum=codex"`
	// Already is true when it had it already.
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
		Short: "Register this app's MCP server with Claude Code and Codex, whichever are installed, for every folder",
		Long: "Runs `claude mcp add --scope user " + product.Command + " -- <" + product.Command + "> mcp serve` when Claude " +
			"Code is installed, and `codex mcp add " + product.Command + " -- <" + product.Command + "> mcp serve` when Codex " +
			"is, so their sessions list the app's tools in every folder, by a path that survives updates. Nothing " +
			"changes where it's registered already; one whose command is gone is replaced. Setup does it on the " +
			"claude-code and codex backends.",
		Annotations: map[string]string{annNotReadOnly: "it changes Claude Code's and Codex's own config"},
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
			out := MCPConnectOutput{Server: product.Command, Command: command, Clients: []MCPClient{}}
			var errs []error
			var texts []string
			if path, protocol, err := engine.FindClaudeCode(app); err == nil {
				already, err := registerClaude(cmd, path, protocol, command)
				if err != nil {
					errs = append(errs, err)
				} else {
					out.Clients = append(out.Clients, MCPClient{Name: "claude-code", Already: already})
					texts = append(texts, connectedText("Claude Code", already, command, "/mcp lists it; a running session needs a restart"))
				}
			}
			if path, protocol, err := engine.FindCodex(app); err == nil {
				already, err := registerCodex(cmd, path, protocol, command)
				engine.ForgetCodexMCP()
				if err != nil {
					errs = append(errs, err)
				} else {
					out.Clients = append(out.Clients, MCPClient{Name: "codex", Already: already})
					texts = append(texts, connectedText("Codex", already, command, "a new session lists its tools"))
				}
			}
			if len(errs) > 0 {
				return errors.Join(errs...)
			}
			if len(out.Clients) == 0 {
				return errors.New("neither Claude Code (`claude`) nor Codex (`codex`) is installed, or a version the app works with: install one, then try again")
			}
			out.Already = true
			for _, c := range out.Clients {
				out.Already = out.Already && c.Already
			}
			return emit(cmd, out, strings.Join(texts, "\n"))
		},
	}
	jsonFlag(cmd.Flags(), MCPConnectOutput{})
	return cmd
}

func connectedText(cli string, already bool, command []string, note string) string {
	if already {
		return cli + " already has " + product.Command + "'s MCP server."
	}
	return "Connected: " + cli + " runs `" + strings.Join(command, " ") + "` for its " + product.Command + " MCP server (" + note + ")."
}

// registerClaude registers the server with Claude Code at user scope;
// already is true when it was there.
func registerClaude(cmd *cobra.Command, path string, protocol agent.ClaudeCodeProtocol, command []string) (already bool, err error) {
	problem, err := agent.ClaudeMCPProblem(protocol, product.Command)
	if err != nil || problem == "" {
		return err == nil, err
	}
	if _, there, _ := protocol.MCPServer(product.Command); there {
		if err := runClaude(cmd.Context(), path, protocol.MCPRemoveArgs(product.Command)...); err != nil {
			return false, err
		}
	}
	if err := runClaude(cmd.Context(), path, protocol.MCPAddArgs(product.Command, command)...); err != nil {
		return false, err
	}
	if problem, err := agent.ClaudeMCPProblem(protocol, product.Command); err != nil {
		return false, err
	} else if problem != "" {
		return false, errors.New("claude mcp add didn't register it: " + problem)
	}
	return false, nil
}

// registerCodex registers the server in Codex's config; already is true
// when it was there.
func registerCodex(cmd *cobra.Command, path string, protocol agent.CodexProtocol, command []string) (already bool, err error) {
	ctx := cmd.Context()
	out, err := agent.RunCodex(ctx, path, protocol.MCPListArgs()...)
	if err != nil {
		return false, err
	}
	s, there, err := protocol.MCPServer(out, product.Command)
	if err != nil {
		return false, err
	}
	if there && (s.Command == "" || !filepath.IsAbs(s.Command) || isFile(s.Command)) {
		return true, nil
	}
	if there {
		if _, err := agent.RunCodex(ctx, path, protocol.MCPRemoveArgs(product.Command)...); err != nil {
			return false, err
		}
	}
	if _, err := agent.RunCodex(ctx, path, protocol.MCPAddArgs(product.Command, command)...); err != nil {
		return false, err
	}
	if problem, err := agent.CodexMCPProblem(ctx, path, protocol, product.Command); err != nil {
		return false, err
	} else if problem != "" {
		return false, errors.New("codex mcp add didn't register it: " + problem)
	}
	return false, nil
}
