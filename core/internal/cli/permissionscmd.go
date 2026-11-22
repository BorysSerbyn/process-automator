package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// Permissions are what the OS must allow before something can work: what
// plugins, programs, program hooks and watchers declare. The scheduler
// checks them (macOS answers it as the app) and asks for one on request;
// these commands show what it recorded, and make the request.

func newPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "What the OS must allow before routines, hooks and watchers can work, and asking it for one",
		Long: "Plugins, programs, program hooks and watchers declare the permissions they need (input-monitoring, " +
			"notifications, …); a prompt needs those of the plugins it uses. The scheduler checks them with the OS on every " +
			"pass, as the app (a check from a terminal would answer for the terminal), and records what it found; one that " +
			"isn't granted keeps what needs it from running, saying so.",
	}
	cmd.AddCommand(newPermissionsListCmd(), newPermissionsRequestCmd())
	return cmd
}

func newPermissionsListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List the permissions something needs, what needs each, and what the scheduler last found",
		Long:        "Each permission something installed needs (--all: every permission), with its state as the scheduler last recorded it: granted, missing, unknown (it can't be checked), unsupported (not on this OS) or not-checked (nothing recorded yet), what needs it and where it's turned on.",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			out, err := a.PermissionReport(all)
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			w := cmd.OutOrStdout()
			if out.CheckedAt == nil {
				fmt.Fprintln(w, "The scheduler hasn't checked any permission yet: it does when it runs.")
			} else {
				fmt.Fprintf(w, "As the scheduler found them at %s.\n", out.CheckedAt.Local().Format("Mon 15:04:05"))
			}
			if len(out.Permissions) == 0 {
				fmt.Fprintln(w, "Nothing installed needs a permission.")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "PERMISSION\tSTATE\tNEEDED BY")
			for _, p := range out.Permissions {
				var by []string
				for _, u := range p.NeededBy {
					by = append(by, u.Kind+" "+u.Name)
				}
				if len(by) == 0 {
					by = []string{"(nothing)"}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Label, permissionStateText(p.State), strings.Join(by, ", "))
				if p.Blocks() && p.State != permissions.NotChecked && len(p.NeededBy) > 0 {
					fmt.Fprintf(tw, "\t\tfix: %s\n", engine.PermissionFix(p.PermissionNeed))
				}
			}
			return tw.Flush()
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every permission, not only those something needs")
	jsonFlag(cmd.Flags(), engine.PermissionReport{})
	return cmd
}

func newPermissionsRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request <permission>",
		Short: "Ask the OS for a permission, as the app (macOS shows its prompt naming the app, the first time)",
		Long: "Asks the scheduler to ask the OS for the permission: macOS attributes the scheduler to the app, so its prompt " +
			"names the app (asked from a terminal, it would name the terminal). macOS asks only once; after that, and for a " +
			"permission an app can't ask for (full-disk-access, automation), it's turned on in System Settings: the output says " +
			"where, with the address that opens it. Waits briefly for the answer, then prints what the scheduler records.",
		Example:     "  " + product.Command + " permissions request input-monitoring",
		Annotations: map[string]string{annNotReadOnly: "it makes the OS ask me for a permission"},
		Args:        cobra.ExactArgs(1),
		ValidArgs:   permissions.Names(),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			out, err := a.RequestPermission(cmd.Context(), args[0], time.Now())
			if err != nil {
				return err
			}
			var lines []string
			switch {
			case out.State == permissions.Granted:
				lines = append(lines, out.Label+" is allowed for "+product.Name+".")
			case out.Asked:
				lines = append(lines, "Asked the OS for "+out.Label+" for "+product.Name+": answer its prompt, if it shows one.")
			default:
				lines = append(lines, "Couldn't ask the OS for "+out.Label+": "+out.Why+".")
			}
			if out.State != permissions.Granted && out.Where != "" {
				turnOn := "Otherwise, turn it on for " + product.Name + " " + out.Where
				if out.SettingsURL != "" {
					turnOn += ": " + out.SettingsURL
				}
				lines = append(lines, turnOn)
			}
			return emit(cmd, out, strings.Join(lines, "\n"))
		}),
	}
	jsonFlag(cmd.Flags(), engine.PermissionRequest{})
	return cmd
}
