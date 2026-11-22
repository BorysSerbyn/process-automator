package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// A plugin's trial starts its server, does the MCP handshake and checks its
// tools: what a routine would get from it, before anything relies on it.

// trialTimeout bounds a trial: starting the server (a first `npx`/`uvx` run
// downloads packages), the handshake and listing tools.
const trialTimeout = 90 * time.Second

// PluginTool is one of a plugin's tools, as a routine would see it.
type PluginTool struct {
	Name string `json:"name"`
	// ReadOnly tools run in dry runs; the others are writes (intercepted).
	ReadOnly bool `json:"readOnly"`
	// Annotated is whether the tool declares annotations (readOnlyHint) at all.
	Annotated bool `json:"annotated"`
}

// PluginTrial is what trying a plugin found.
type PluginTrial struct {
	Tools []PluginTool `json:"tools,omitempty"`
	// NeedsSignIn means it couldn't be tried further without credentials
	// (`auth login <name>`, then `plugins check <name>`).
	NeedsSignIn bool     `json:"needsSignIn,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// tryPlugin connects to a server (spec, or the configured one when nil) and
// checks its tools. strict (plugin folders) also requires every tool to
// declare annotations; otherwise undeclared ones are a warning, since they're
// treated as writes anyway.
func tryPlugin(ctx context.Context, a *engine.App, name string, spec *pluginapi.ServerSpec, strict bool) (PluginTrial, error) {
	var t PluginTrial
	conn := a.Connector
	if spec != nil {
		c := *a.Connector
		c.Servers = map[string]pluginapi.ServerSpec{}
		for k, v := range a.Connector.Servers {
			c.Servers[k] = v
		}
		c.Servers[name] = *spec
		conn = &c
	}
	ctx, cancel := context.WithTimeout(ctx, trialTimeout)
	defer cancel()
	s, err := conn.Connect(ctx, name)
	if errors.Is(err, mcp.ErrNeedsReauth) {
		t.NeedsSignIn = true
		// Not signed in yet, so the server can't be tried; at least its URL must answer.
		if spec != nil && config.Remote(*spec) {
			if err := probeURL(ctx, spec.URL); err != nil {
				return t, fmt.Errorf("can't reach %s: %w", spec.URL, err)
			}
		}
		return t, nil
	}
	if err != nil {
		return t, fmt.Errorf("doesn't start or answer: %w", err)
	}
	defer func() { _ = s.Close() }()
	list, err := s.ListTools(ctx, nil)
	if err != nil {
		return t, fmt.Errorf("can't list its tools: %w", err)
	}
	var problems, unannotated []string
	seen := map[string]bool{}
	for _, tool := range list.Tools {
		if seen[tool.Name] {
			problems = append(problems, fmt.Sprintf("tool %q is listed twice", tool.Name))
		}
		seen[tool.Name] = true
		if err := checkInputSchema(tool.InputSchema); err != nil {
			problems = append(problems, fmt.Sprintf("tool %q: %v", tool.Name, err))
		}
		pt := PluginTool{Name: tool.Name, Annotated: tool.Annotations != nil}
		if pt.Annotated {
			pt.ReadOnly = tool.Annotations.ReadOnlyHint
		} else {
			unannotated = append(unannotated, tool.Name)
		}
		t.Tools = append(t.Tools, pt)
	}
	sort.Slice(t.Tools, func(i, j int) bool { return t.Tools[i].Name < t.Tools[j].Name })
	if len(list.Tools) == 0 {
		problems = append(problems, "it offers no tools")
	}
	if len(unannotated) > 0 {
		msg := "tools without annotations: " + strings.Join(unannotated, ", ")
		if strict {
			problems = append(problems, msg+" (declare readOnlyHint on every tool: true for reads, false for writes; dry runs depend on it)")
		} else {
			t.Warnings = append(t.Warnings, msg+" (treated as writes: dry runs intercept them)")
		}
	}
	if len(problems) > 0 {
		return t, errors.New(strings.Join(problems, "; "))
	}
	return t, nil
}

// probeURL checks a URL answers HTTP at all (any status: an MCP server
// wanting sign-in answers 401).
func probeURL(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// checkInputSchema requires an object schema, as MCP does.
func checkInputSchema(schema any) error {
	b, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return errors.New("input schema isn't a JSON object")
	}
	if m["type"] != "object" {
		return fmt.Errorf("input schema type is %v, not object", m["type"])
	}
	return nil
}

// manifestFile describes how to start a plugin folder's server.
const manifestFile = "plugin.yaml"

// pluginManifest is a plugin folder's plugin.yaml. Each field's guide tag is
// what it means, for `guide plugins`.
type pluginManifest struct {
	Description string            `yaml:"description" guide:"What the server is for, shown by plugins list."`
	Command     string            `yaml:"command" guide:"Required. The command that starts the server (stdio). A relative path (./server) resolves inside the folder; otherwise an absolute path, or a command on PATH such as python3 or npx."`
	Args        []string          `yaml:"args" guide:"The command's arguments."`
	Env         map[string]string `yaml:"env" guide:"Its environment, NAME: value. A value may reference a secret as {key}, which auth login <name> asks for."`
	Pairing     string            `yaml:"pairing" guide:"For a server that issues its own credential on first contact (an approval dialog, then a key shown once): what to do to approve it. auth login then starts the server first, shows this, and asks for the credential, which must be a secret its env references."`
}

func readManifest(dir string) (pluginManifest, error) {
	var m pluginManifest
	raw, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return m, fmt.Errorf("a plugin folder needs a %s (command, args, env, description): %w", manifestFile, err)
	}
	if err := yamlfile.DecodeStrict(raw, &m); err != nil {
		return m, fmt.Errorf("%s: %w", manifestFile, err)
	}
	if m.Command == "" {
		return m, fmt.Errorf("%s: command is required", manifestFile)
	}
	if c := m.Command; !filepath.IsAbs(c) && strings.ContainsRune(c, filepath.Separator) && !insideDir(".", c) {
		return m, fmt.Errorf("%s: command %q points outside the folder", manifestFile, c)
	}
	return m, nil
}

// insideDir reports whether rel, relative to base, stays inside base.
func insideDir(base, rel string) bool {
	r, err := filepath.Rel(base, filepath.Join(base, rel))
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// spec is the server for the manifest's folder installed at dir. A relative
// command path (./server) is resolved inside the folder.
func (m pluginManifest) spec(dir string) pluginapi.ServerSpec {
	cmd := m.Command
	if !filepath.IsAbs(cmd) && strings.ContainsRune(cmd, filepath.Separator) {
		cmd = filepath.Join(dir, cmd)
	}
	return pluginapi.ServerSpec{Description: m.Description, Command: cmd, Args: m.Args, Env: m.Env, Pairing: m.Pairing, Dir: dir}
}

// managed reports whether a server runs from a plugin folder the app
// installed: exactly one level inside the plugins folder, under a valid name.
func managed(a *engine.App, s pluginapi.ServerSpec) bool {
	if s.Dir == "" {
		return false
	}
	dir := filepath.Clean(s.Dir)
	return filepath.Dir(dir) == a.Layout.Plugins() && config.ValidName(filepath.Base(dir))
}

// installFolder copies a plugin folder to a staging copy, tries it from
// there, and only then moves it into place, keeping an older copy aside. On
// any failure nothing changes. finish(true) drops the older copy once the
// config points at the new one; finish(false) puts the older copy back.
func installFolder(ctx context.Context, a *engine.App, name, src string) (spec pluginapi.ServerSpec, trial PluginTrial, finish func(ok bool), err error) {
	if src, err = filepath.Abs(src); err != nil {
		return spec, trial, nil, err
	}
	if src, err = filepath.EvalSymlinks(src); err != nil { // the folder itself may be a link
		return spec, trial, nil, err
	}
	m, err := readManifest(src)
	if err != nil {
		return spec, trial, nil, err
	}
	if err := os.MkdirAll(a.Layout.Plugins(), 0o700); err != nil {
		return spec, trial, nil, err
	}
	staging, err := os.MkdirTemp(a.Layout.Plugins(), ".staging-"+name+"-")
	if err != nil {
		return spec, trial, nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }() // gone once moved into place
	if err := copyTree(src, staging); err != nil {
		return spec, trial, nil, fmt.Errorf("copy %s: %w", src, err)
	}
	staged := m.spec(staging)
	if err := config.ValidateServer(name, staged); err != nil {
		return spec, trial, nil, err
	}
	if trial, err = tryPlugin(ctx, a, name, &staged, true); err != nil {
		return spec, trial, nil, fmt.Errorf("trial failed, nothing installed: %w", err)
	}
	final := filepath.Join(a.Layout.Plugins(), name)
	old := final + ".old"
	_ = os.RemoveAll(old)
	hadOld := false
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, old); err != nil {
			return spec, trial, nil, err
		}
		hadOld = true
	}
	if err := os.Rename(staging, final); err != nil {
		if hadOld {
			_ = os.Rename(old, final)
		}
		return spec, trial, nil, err
	}
	finish = func(ok bool) {
		if ok {
			_ = os.RemoveAll(old)
			return
		}
		_ = os.RemoveAll(final)
		if hadOld {
			_ = os.Rename(old, final)
		}
	}
	return m.spec(final), trial, finish, nil
}

// copyTree copies a folder (files, folders and symlinks, keeping modes),
// leaving out .git. Symlinks must stay inside the folder, so the copy never
// depends on where it came from.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !insideDir(src, filepath.Join(filepath.Dir(rel), link)) {
				return fmt.Errorf("symlink %s points outside the folder (%s); replace it with a copy", rel, link)
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, info.Mode().Perm())
		}
		return nil // sockets, devices: not part of a plugin
	})
}

// PluginCheck is one row of `plugins check --json`.
type PluginCheck struct {
	Plugin string `json:"plugin"`
	OK     bool   `json:"ok"`
	// Problem is why routines couldn't use it (doesn't start, not signed in, bad tools).
	Problem string `json:"problem,omitempty"`
	PluginTrial
}

func newPluginsCheckCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check [<name>]",
		Short: "Try plugins: start each server, check its tools, report what routines would get",
		Long: "Connects to each plugin (or one) the way a routine would, lists its tools and checks them. " +
			"Plugins installed from a folder must declare annotations on every tool. Fails if any plugin " +
			"doesn't start, isn't signed in, or has broken tools.",
		Annotations: readOnly(),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			var names []string
			if len(args) == 1 {
				names = args
			} else {
				for _, p := range a.Plugins {
					if p.InProcess != nil {
						names = append(names, p.Name)
					}
				}
				for n := range a.Config.MCPServers {
					names = append(names, n)
				}
				sort.Strings(names)
			}
			var rows []PluginCheck
			failed := 0
			for _, name := range names {
				p, found := a.FindPlugin(name)
				inProcess := found && p.InProcess != nil
				spec, configured := a.Config.MCPServers[name]
				row := PluginCheck{Plugin: name}
				switch {
				case !configured && !inProcess:
					row.Problem = "no such plugin"
				default:
					row.PluginTrial, err = tryPlugin(cmd.Context(), a, name, nil, configured && managed(a, spec))
					switch {
					case err != nil:
						row.Problem = err.Error()
					case row.NeedsSignIn:
						row.Problem = "not signed in: " + product.Command + " auth login " + name
					}
				}
				row.OK = row.Problem == ""
				if !row.OK {
					failed++
				}
				rows = append(rows, row)
			}
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), rows); err != nil {
					return err
				}
			} else {
				for _, r := range rows {
					if r.OK {
						fmt.Fprintf(cmd.OutOrStdout(), "ok    %s (%s)\n", r.Plugin, toolSummary(r.Tools))
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "FAIL  %s: %s\n", r.Plugin, r.Problem)
					}
					for _, w := range r.Warnings {
						fmt.Fprintf(cmd.OutOrStdout(), "      warning: %s\n", w)
					}
				}
			}
			if failed > 0 {
				return reported(fmt.Errorf("%d of %d plugins have problems", failed, len(rows)))
			}
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, []PluginCheck{})
	return cmd
}

// toolSummary is "3 tools: 2 read-only, 1 write".
func toolSummary(tools []PluginTool) string {
	ro := 0
	for _, t := range tools {
		if t.ReadOnly {
			ro++
		}
	}
	return fmt.Sprintf("%d tools: %d read-only, %d write", len(tools), ro, len(tools)-ro)
}
