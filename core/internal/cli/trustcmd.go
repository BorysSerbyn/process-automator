package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
)

// A routine runs for real only while what it can reach is what I trusted
// (see package trust). Dry runs never need trust.

// TrustOutput is `routines trust --json`.
type TrustOutput struct {
	Routine string        `json:"routine"`
	Surface trust.Surface `json:"surface"`
	// Tools are its allowlisted tools with the server offering each and
	// whether it's read-only (absent when its servers couldn't be reached).
	Tools []agent.CheckedTool `json:"tools,omitempty"`
	// ToolsError says why its tools couldn't be checked (e.g. a server needs
	// a sign-in).
	ToolsError string `json:"toolsError,omitempty"`
	// Changes since it was last trusted (empty for a first trust).
	Changes []string `json:"changes"`
	Trusted bool     `json:"trusted"`
}

func newRoutinesTrustCmd() *cobra.Command {
	var yes, check, asJSON bool
	cmd := &cobra.Command{
		Use:   "trust <routine>",
		Short: "Review what a routine can reach, and trust it to run for real",
		Long: "Shows the routine's servers, its tools (which ones write), built-in tools, plugin settings and folder, " +
			"and what changed since you last trusted it, then asks. A routine runs for real only while what it can " +
			"reach is what you trusted; a change to it (a new server or tool, another folder, ...) takes the trust " +
			"away until you trust it again. Its prompt, schedule and other settings don't. Dry runs never need it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, unlock, err := loadAppForEdit(cmd)
			if err != nil {
				return err
			}
			defer unlock()
			set, err := routines.Load(a.Layout.Routines())
			if err != nil {
				return err
			}
			r, err := set.Find(args[0])
			if err != nil {
				return err
			}
			c, err := a.RoutineConfig(r)
			if err != nil {
				return err
			}
			f, _, err := a.TrustStore().Load()
			if err != nil {
				return err
			}
			out := TrustOutput{Routine: r.Name, Surface: engine.SurfaceOf(r, c).Normalized(), Changes: []string{}}
			if rec, ok := f.Routines[r.Name]; ok {
				out.Changes = append(out.Changes, out.Surface.Changes(rec.Surface)...)
				if len(out.Changes) == 0 {
					out.Trusted = true
					if !check {
						return emit(cmd, out, r.Name+" is already trusted with what it can reach now.")
					}
				}
			}
			tools, toolsErr := agent.CheckTools(cmd.Context(), a.Connector, engine.RunOf(r, c.Folder, ""), r.Servers, r.Tools)
			out.Tools = tools
			if toolsErr != nil {
				out.ToolsError = toolsErr.Error()
			}
			if !asJSON {
				describeSurface(cmd, out, toolsErr)
			}
			if check {
				note := "\nNothing trusted (--check only reviews)."
				if out.Trusted {
					note = "\nIt's trusted with what it can reach now."
				}
				return emit(cmd, out, note)
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return errors.New("trusting is your decision: run it in a terminal, or add --yes after reviewing the above")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\nTrust %s to run for real with this? [y/N] ", r.Name)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
					return errors.New("not trusted")
				}
			}
			if err := a.TrustStore().Trust(map[string]trust.Surface{r.Name: engine.SurfaceOf(r, c)}, time.Now()); err != nil {
				return err
			}
			out.Trusted = true
			return emit(cmd, out, "Trusted "+r.Name+".")
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "trust without asking (after reviewing what it can reach)")
	cmd.Flags().BoolVar(&check, "check", false, "only show what it can reach and what changed; trust nothing (frontends review with it, then trust with --yes)")
	cmd.MarkFlagsMutuallyExclusive("check", "yes")
	jsonFlag(cmd.Flags(), &asJSON, TrustOutput{})
	return cmd
}

// describeSurface prints what a routine can reach, writes first.
func describeSurface(cmd *cobra.Command, out TrustOutput, toolsErr error) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s can reach:\n", out.Routine)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	none := func(l []string) string {
		if len(l) == 0 {
			return "(none)"
		}
		return strings.Join(l, ", ")
	}
	fmt.Fprintf(tw, "  servers\t%s\n", none(out.Surface.Servers))
	if toolsErr != nil {
		fmt.Fprintf(tw, "  tools\t%s\t(couldn't check which write: %v)\n", none(out.Surface.Tools), toolsErr)
	} else {
		var writes, reads []string
		for _, t := range out.Tools {
			if t.ReadOnly {
				reads = append(reads, t.Name)
			} else {
				writes = append(writes, t.Name+" ("+t.Server+")")
			}
		}
		fmt.Fprintf(tw, "  writes\t%s\n", none(writes))
		fmt.Fprintf(tw, "  reads\t%s\n", none(reads))
	}
	if len(out.Surface.BuiltinTools) > 0 {
		fmt.Fprintf(tw, "  built-in tools\t%s\n", none(out.Surface.BuiltinTools))
	}
	folder := out.Surface.Folder
	if folder == "" {
		folder = "(an empty temporary folder)"
	}
	fmt.Fprintf(tw, "  folder\t%s\n", folder)
	for name, section := range out.Surface.Plugins {
		fmt.Fprintf(tw, "  %s plugin\t%v\n", name, section)
	}
	_ = tw.Flush()
	if len(out.Changes) > 0 {
		fmt.Fprintf(w, "\nSince you last trusted it, it %s.\n", strings.Join(out.Changes, "; "))
	}
}
