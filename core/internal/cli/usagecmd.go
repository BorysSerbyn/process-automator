package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/statusline"
	"github.com/BorysSerbyn/process-automator/core/internal/usage"
)

func newUsageCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "LLM usage against its limit: the Claude plan's usage (claude-code) or API spend (claude-api); none on codex",
		Long: "On a Claude plan (claude-code), the plan's 5-hour and weekly usage against llm.usage-limit, as " +
			"Claude Code's /usage reports it (read again when the last reading is over 10 minutes old; no tokens " +
			"used); the app never works out a cost for this backend. With an API key (claude-api), what runs spent at " +
			"list prices against the " +
			"llm.budget.* budgets. Past the limit, scheduled runs don't start and are recorded as skipped; runs " +
			"started by hand still run. On codex the app can't read Codex's usage, so there's no limit (kind none).",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := loadApp(cmd)
			if err != nil {
				return err
			}
			r, err := a.Usage(cmd.Context(), time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), r)
			}
			w := cmd.OutOrStdout()
			switch r.Kind {
			case "plan":
				printPlan(w, r)
			case usage.KindNone:
				fmt.Fprintln(w, "No usage limit with this backend: the app can't read its usage, and its runs have no cost.")
			default:
				printBudget(w, r)
			}
			if r.Over {
				fmt.Fprintf(w, "\nScheduled runs don't start: %s.\n", r.Reason)
			}
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &asJSON, usage.Report{})
	cmd.AddCommand(newStatusLineCmd())
	return cmd
}

func printPlan(w io.Writer, r usage.Report) {
	fmt.Fprintf(w, "Claude plan usage (scheduled runs stop past %g%%):\n", r.LimitPercent)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, win := range r.Windows {
		if win.UsedPercent == nil {
			fmt.Fprintf(tw, "  %s\tnot known\n", win.Length)
			continue
		}
		fmt.Fprintf(tw, "  %s\t%.0f%%\tresets %s\n", win.Length, *win.UsedPercent, win.ResetsAt.Local().Format("Mon 15:04"))
	}
	_ = tw.Flush()
	for _, m := range r.Models {
		fmt.Fprintf(w, "  (%s weekly: %.0f%%, not applied)\n", m.Model, m.UsedPercent)
	}
	if r.FetchedAt != nil {
		fmt.Fprintf(w, "As of %s.\n", r.FetchedAt.Local().Format("Mon 15:04"))
	}
	if r.ReadError != "" {
		fmt.Fprintf(w, "Couldn't read it just now: %s\n", r.ReadError)
	}
}

func printBudget(w io.Writer, r usage.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WINDOW\tSPENT\tBUDGET")
	for _, win := range r.Windows {
		budget := "none"
		if *win.BudgetUSD > 0 {
			budget = fmt.Sprintf("$%.2f", *win.BudgetUSD)
		}
		fmt.Fprintf(tw, "last %s\t$%.2f\t%s\n", win.Length, *win.SpentUSD, budget)
	}
	_ = tw.Flush()
}

// StatusLineOutput is `usage statusline install|uninstall --json`.
type StatusLineOutput struct {
	// Installed is whether Claude Code's status line runs the app's command.
	Installed bool `json:"installed"`
	// Wraps is the status line command of mine the app runs (empty: none).
	Wraps string `json:"wraps,omitempty"`
}

func newStatusLineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "statusline",
		Short: "Claude Code runs this as its status line: it records the plan's usage, then runs yours",
		Long: "While the app can't read the Claude plan's usage from Claude Code's /usage, it reads it from Claude " +
			"Code's status line instead. `usage statusline install` makes Claude Code run this command as its " +
			"status line (in ~/.claude/settings.json, changing nothing else); it runs the status line you had with " +
			"the same input, so it looks the same. `uninstall` puts yours back. Run with no subcommand, it's the " +
			"status line itself: it reads Claude Code's input on stdin, and never fails.",
		Annotations: map[string]string{annNotReadOnly: "it's Claude Code's status line"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, _ := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			a, err := loadApp(cmd)
			if err != nil {
				return nil // a status line mustn't fail over this
			}
			_ = usage.RecordStatusLine(a.Layout.PlanUsage(), in, time.Now())
			if mine, _ := statusline.Original(a.Layout.StatusLine()); mine != "" {
				_ = runShell(mine, bytes.NewReader(in), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			return nil
		},
	}
	switchCmd := func(install bool) *cobra.Command {
		var asJSON bool
		use, short := "uninstall", "Put back the Claude Code status line you had"
		if install {
			use, short = "install", "Have Claude Code's status line record the plan's usage (yours keeps showing)"
		}
		c := &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			Annotations: map[string]string{annNotReadOnly: "it changes Claude Code's settings"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				a, err := loadApp(cmd)
				if err != nil {
					return err
				}
				settingsPath, err := statusline.SettingsPath()
				if err != nil {
					return err
				}
				if install {
					command, cerr := statusLineCommand(a)
					if cerr != nil {
						return cerr
					}
					err = statusline.Install(settingsPath, a.Layout.StatusLine(), command)
				} else {
					err = statusline.Uninstall(settingsPath, a.Layout.StatusLine())
				}
				if err != nil {
					return err
				}
				out := StatusLineOutput{}
				out.Installed, _ = statusline.Installed(settingsPath)
				out.Wraps, _ = statusline.Original(a.Layout.StatusLine())
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), out)
				}
				switch {
				case out.Installed && out.Wraps != "":
					fmt.Fprintf(cmd.OutOrStdout(), "Claude Code's status line now records the plan's usage, and still shows yours (%s).\n", out.Wraps)
				case out.Installed:
					fmt.Fprintln(cmd.OutOrStdout(), "Claude Code's status line now records the plan's usage (it shows nothing).")
				default:
					fmt.Fprintln(cmd.OutOrStdout(), "Your Claude Code status line is back as it was.")
				}
				return nil
			},
		}
		jsonFlag(c.Flags(), &asJSON, StatusLineOutput{})
		return c
	}
	cmd.AddCommand(switchCmd(true), switchCmd(false))
	return cmd
}

// statusLineCommand is the command Claude Code runs as its status line: this
// binary, with this config when it isn't the default one.
func statusLineCommand(a *engine.App) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	command := shellQuote(exe)
	if def, err := config.DefaultPath(); err != nil || def != a.Layout.Config() {
		command += " --config " + shellQuote(a.Layout.Config())
	}
	return command + " " + statusline.Marker, nil
}

// shellQuote quotes a word for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
