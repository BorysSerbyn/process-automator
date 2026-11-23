package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
)

// watchPoll is how often `status --watch` looks for a new status: a stat of
// the file, so it costs next to nothing, and a click (Run Now turning into
// Stop) shows at once.
var watchPoll = 250 * time.Millisecond

func newStatusCmd() *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the app's status: health, why, and each routine's last and next run",
		Long: "The run status the scheduler keeps: health (ok, error, none) with the reasons, whether a routine " +
			"is running, and each routine's last and next run. Past validUntil the scheduler isn't responding. " +
			"--watch prints it again, one JSON line each time it changes, until stopped (frontends follow it so).",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			path := a.Layout.RunStatus()
			if watch {
				return watchStatus(cmd.Context(), path, cmd.OutOrStdout())
			}
			s, err := runstatus.Load(path)
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), s)
			}
			printStatus(cmd.OutOrStdout(), s)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "print it again, as one JSON line, each time it changes (until stopped)")
	hideFromMCP(cmd, "watch") // it never ends
	jsonFlag(cmd.Flags(), runstatus.Status{})
	return cmd
}

// watchStatus writes the status as a JSON line now and whenever the file
// changes, until ctx ends or the reader goes away.
func watchStatus(ctx context.Context, path string, w io.Writer) error {
	var last os.FileInfo
	first := true
	t := time.NewTicker(watchPoll)
	defer t.Stop()
	for {
		info, err := os.Stat(path)
		if err != nil {
			info = nil // missing for now: an empty status
		}
		changed := first || (info == nil) != (last == nil) ||
			info != nil && (!info.ModTime().Equal(last.ModTime()) || info.Size() != last.Size())
		if changed {
			s, err := runstatus.Load(path)
			if err == nil { // a torn read is retried on the next tick
				b, err := json.Marshal(s)
				if err != nil {
					return err
				}
				if _, err := w.Write(append(b, '\n')); err != nil {
					return nil // nobody's reading any more
				}
				first, last = false, info
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// printStatus is the status in words.
func printStatus(w io.Writer, s *runstatus.Status) {
	summary := s.Summary
	if summary == "" {
		summary = string(s.Health)
	}
	fmt.Fprintf(w, "%s: %s\n", s.Health, summary)
	if s.ValidUntil == nil || time.Now().After(*s.ValidUntil) {
		fmt.Fprintf(w, "The scheduler isn't responding (no recent heartbeat): `%s login-items start` starts it, `%s doctor` says more.\n",
			product.Command, product.Command)
	}
	if s.Problem != "" {
		fmt.Fprintln(w, "Problem:", s.Problem)
	}
	for _, r := range s.Reasons {
		fmt.Fprintln(w, "-", r.Text)
	}
}
