package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/schedule"
)

// scheduleNext is how many upcoming times `schedule` lists.
const scheduleNext = 3

func newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Routine schedules in plain words, and a days-and-time picker as cron (for frontends)",
		Long: "A routine's schedule is a cron expression (routines.<name>.schedule). describe says what one means " +
			"and when it next runs, and reads it as days and a time when it is one; build turns days and a time " +
			"into one. Neither changes anything: set the result with `settings set`.",
	}
	var asJSON bool
	jsonFlag(cmd.PersistentFlags(), &asJSON, schedule.Info{})
	show := func(cmd *cobra.Command, info schedule.Info) error {
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), info)
		}
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%s\n%s\n", info.Cron, info.Text)
		for _, t := range info.Next {
			fmt.Fprintf(w, "  next %s\n", t.Local().Format("Mon Jan 2 15:04"))
		}
		return nil
	}
	describe := &cobra.Command{
		Use:         "describe <cron>",
		Short:       "Say what a schedule means and when it next runs",
		Example:     "  procauto schedule describe '20 9 * * 1-5'",
		Annotations: readOnly(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := schedule.Describe(args[0], time.Now(), scheduleNext)
			if err != nil {
				return err
			}
			return show(cmd, info)
		},
	}
	var days []string
	var at string
	build := &cobra.Command{
		Use:         "build",
		Short:       "Turn days of the week and a time into a schedule",
		Example:     "  procauto schedule build --days mon,tue,wed,thu,fri --at 09:20",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			expr, err := schedule.Build(schedule.Picker{Days: days, At: at})
			if err != nil {
				return err
			}
			info, err := schedule.Describe(expr, time.Now(), scheduleNext)
			if err != nil {
				return err
			}
			return show(cmd, info)
		},
	}
	build.Flags().StringSliceVar(&days, "days", nil, "days it runs: "+strings.Join(schedule.Days, ", "))
	build.Flags().StringVar(&at, "at", "", "time of day, HH:MM (24-hour)")
	_ = build.MarkFlagRequired("days")
	_ = build.MarkFlagRequired("at")
	cmd.AddCommand(describe, build)
	return cmd
}
