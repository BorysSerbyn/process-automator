package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
)

func newRunsCmd() *cobra.Command {
	var since string
	var limit int
	cmd := &cobra.Command{
		Use:         "runs [<routine>]",
		Annotations: readOnly(),
		Short:       "List past runs (which ran, how they ended and why), newest first",
		Example: "  " + product.Command + " runs\n  " + product.Command + " runs standup --since 7d\n" +
			"  " + product.Command + " runs show standup/2026-11-19T092022",
		Args: cobra.MaximumNArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			var err error
			f := history.Filter{Limit: limit}
			if len(args) == 1 {
				f.Routine = args[0]
			}
			if since != "" {
				if f.Since, err = parseSince(since, time.Now()); err != nil {
					return err
				}
			}
			runs, err := history.List(a.Layout.History(), f)
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				if runs == nil {
					runs = []history.Summary{}
				}
				return writeJSON(cmd.OutOrStdout(), runs)
			}
			out := cmd.OutOrStdout()
			if len(runs) == 0 {
				fmt.Fprintln(out, "No runs.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTARTED\tTRIGGER\tOUTCOME\tREASON")
			for _, r := range runs {
				trig := r.Trigger
				if r.Trigger == trigger.Backfill && r.For != nil {
					trig += " " + r.For.Local().Format("Jan 2")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.StartedAt.Local().Format("Mon Jan 2 15:04"),
					trig, r.Outcome, oneLine(r.Reason, 70))
			}
			return tw.Flush()
		}),
	}
	jsonFlag(cmd.Flags(), []history.Summary{})
	cmd.Flags().StringVar(&since, "since", "", "only runs started after this: a duration back (7d, 12h) or a date (2026-11-17)")
	cmd.Flags().IntVar(&limit, "limit", 50, "at most this many runs (0 = all)")
	cmd.AddCommand(newRunsShowCmd())
	return cmd
}

func newRunsShowCmd() *cobra.Command {
	var open bool
	cmd := &cobra.Command{
		Use:         "show <id>",
		Annotations: readOnly(),
		Short:       "Show one run in full: what it covered, how it ended, its tool calls and writes, and its final text",
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			if open {
				path, err := openRunReport(a, args[0])
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), path)
				return nil
			}
			e, err := history.Get(a.Layout.History(), args[0])
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), e)
			}
			printRun(cmd.OutOrStdout(), e)
			return nil
		}),
	}
	jsonFlag(cmd.Flags(), history.Entry{})
	cmd.Flags().BoolVar(&open, "open", false, "open a readable report of the run in the default app (frontends showing a run)")
	hideFromMCP(cmd, "open") // opening on my desktop isn't a client's business
	cmd.MarkFlagsMutuallyExclusive("open", "json")
	return cmd
}

func printRun(w io.Writer, e *history.Entry) {
	fmt.Fprintf(w, "%s\n", e.ID)
	fmt.Fprintf(w, "  trigger   %s\n", e.Trigger)
	if e.For != nil && e.Trigger != trigger.Manual {
		fmt.Fprintf(w, "  for       %s\n", e.For.Local().Format("Mon Jan 2 15:04"))
	}
	fmt.Fprintf(w, "  started   %s\n", e.StartedAt.Local().Format("Mon Jan 2 15:04:05"))
	if e.FinishedAt != nil {
		fmt.Fprintf(w, "  finished  %s (%s)\n", e.FinishedAt.Local().Format("15:04:05"), e.FinishedAt.Sub(e.StartedAt).Round(time.Second))
	}
	fmt.Fprintf(w, "  outcome   %s\n", e.Outcome)
	if e.Result != nil && e.Result.Output != nil {
		if b, err := json.MarshalIndent(e.Result.Output, "            ", "  "); err == nil {
			fmt.Fprintf(w, "  output    %s\n", b)
		}
	}
	if e.SessionID != "" {
		fmt.Fprintf(w, "  session   %s\n", e.SessionID)
	}
	if e.Reason != "" {
		fmt.Fprintf(w, "  reason    %s\n", e.Reason)
	}
	for _, k := range []string{"today", "since"} {
		if v, ok := e.Inputs[k]; ok {
			fmt.Fprintf(w, "  %-9s %v\n", k, v)
		}
	}
	r := e.Result
	if r == nil {
		return
	}
	fmt.Fprintf(w, "\n[%s]\n", r.Summary())
	for _, wr := range r.Writes {
		status := "done"
		switch {
		case r.DryRun:
			status = "intercepted (dry run)"
		case wr.Error != "":
			status = "failed: " + wr.Error
		}
		fmt.Fprintf(w, "  write %s: %s\n", wr.Tool, status)
	}
	if text := strings.TrimSpace(r.Text); text != "" {
		fmt.Fprintf(w, "\n%s\n", text)
	}
}

// parseSince reads "7d", "12h" (back from now) or a date "2026-11-17" (local).
func parseSince(s string, now time.Time) (time.Time, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err == nil && days >= 0 {
			return now.AddDate(0, 0, -days), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--since %q: want a duration (7d, 12h) or a date (2026-11-17)", s)
}

func oneLine(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > width {
		return string([]rune(s)[:width-1]) + "…"
	}
	return s
}

// reportsKept is how long a run report written for opening is kept.
const reportsKept = 7 * 24 * time.Hour

// pruneReports removes reports written more than reportsKept ago: they're
// only for opening, and the run history holds the runs.
func pruneReports(dir string, now time.Time) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > reportsKept {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// openRunReport writes a readable report of a run (as `runs show` prints it)
// and opens it (`runs show --open`).
func openRunReport(a *engine.App, runID string) (string, error) {
	e, err := history.Get(a.Layout.History(), runID)
	if err != nil {
		return "", err
	}
	dir := a.Layout.Reports()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	pruneReports(dir, time.Now())
	path := filepath.Join(dir, strings.ReplaceAll(runID, "/", "_")+".txt")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	printRun(f, e)
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, openExternal(path)
}
