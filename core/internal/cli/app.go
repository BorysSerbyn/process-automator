package cli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
	"github.com/spf13/cobra"
)

// secretStore is where the app keeps secrets (tests swap in a memory store).
var secretStore = func() secrets.Store { return secrets.Default(product.KeychainService()) }

type pluginsKey struct{}

// withPlugins puts the installed plugins in the command context (set by NewRoot).
func withPlugins(ctx context.Context, ps []pluginapi.Plugin) context.Context {
	return context.WithValue(ctx, pluginsKey{}, ps)
}

func pluginsFrom(ctx context.Context) []pluginapi.Plugin {
	ps, _ := ctx.Value(pluginsKey{}).([]pluginapi.Plugin)
	return ps
}

// configPath is the --config flag, or the default config file.
func configPath(cmd *cobra.Command) (string, error) {
	if path, _ := cmd.Flags().GetString("config"); path != "" {
		return path, nil
	}
	return config.DefaultPath()
}

// loadApp loads the app for the command's config, with the installed plugins.
func loadApp(cmd *cobra.Command) (*engine.App, error) {
	path, err := configPath(cmd)
	if err != nil {
		return nil, err
	}
	return engine.Load(path, pluginsFrom(cmd.Context()), secretStore())
}

// loadAppForEdit is loadApp under the edit lock (see engine.LoadForEdit).
func loadAppForEdit(cmd *cobra.Command) (a *engine.App, unlock func(), err error) {
	path, err := configPath(cmd)
	if err != nil {
		return nil, nil, err
	}
	return engine.LoadForEdit(path, pluginsFrom(cmd.Context()), secretStore())
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
