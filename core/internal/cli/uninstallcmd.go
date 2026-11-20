package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/statusline"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
)

// installedApp is the app this binary is part of (the .app, or the Linux
// tree) and its installer, which sits next to the templates two folders
// below the app's root in both layouts. A development build has none.
func installedApp() (root, installer string, err error) {
	templates, err := templatesDir()
	if err != nil {
		return "", "", err
	}
	resources := filepath.Dir(templates)
	installer = filepath.Join(resources, update.InstallerName)
	if _, err := os.Stat(installer); err != nil {
		return "", "", errors.New("this build isn't an installed app (it has no " + update.InstallerName +
			"): install a release with install.sh, or build and install one with `make install`")
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil && strings.Contains(exe, string(filepath.Separator)+"Cellar"+string(filepath.Separator)) {
		return "", "", errors.New("this app was installed by Homebrew: update or remove it with brew " +
			"(`brew upgrade " + product.Folder + "`, `brew uninstall " + product.Folder + "`)")
	}
	return filepath.Dir(filepath.Dir(resources)), installer, nil
}

func newUninstallCmd() *cobra.Command {
	var purge, yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the app, its CLI link and its login items; --purge also your config, routines, history, logs and secrets",
		Long: "Stops the scheduler and automator-ui, removes their login items, the " + product.Command + " link and the app. " +
			"Your config, routines, run history, logs and secrets stay unless you add --purge. Asks first; " +
			"without a terminal it needs --yes. An app installed with Homebrew is removed with brew.",
		Annotations: map[string]string{annNotForRoutines: "removing the app is mine"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			root, installer, err := installedApp()
			if err != nil {
				return err
			}
			what := "the app (" + root + "), its " + product.Command + " link and its login items"
			if purge {
				what += ", and your config, routines, history, logs and secrets (" + a.Layout.Data() + ")"
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return errors.New("uninstalling removes " + what + ": add --yes to go ahead")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "This removes %s. Go ahead? [y/N] ", what)
				var answer string
				_, _ = fmt.Fscanln(cmd.InOrStdin(), &answer)
				if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
					return errors.New("nothing removed")
				}
			}
			if settingsPath, err := statusline.SettingsPath(); err == nil {
				if err := statusline.Uninstall(settingsPath, a.Layout.StatusLine()); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning: putting back your Claude Code status line:", err)
				}
			}
			args := []string{installer, "uninstall", "--config", a.Layout.Config()}
			if purge {
				args = append(args, "--purge")
			}
			c := exec.CommandContext(cmd.Context(), "bash", args...)
			c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return c.Run()
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove config, routines, history, logs and secrets")
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask (needed without a terminal)")
	return cmd
}
