package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/memory"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// Memories are mine: I add them (the CLI, a frontend, or Claude with my
// approval); routines only read them (see package memory).

// scopeFlag is --routine <name> (a routine's own memories; none: every routine's).
type scopeFlag struct{ routine string }

func (f *scopeFlag) add(cmd *cobra.Command, verb string) {
	cmd.Flags().StringVar(&f.routine, "routine", "", verb+" a routine's own memories (default: those every routine gets)")
}

// scope checks the routine exists (memories for a routine that doesn't would
// never be used).
func (f *scopeFlag) scope(a *engine.App) (string, error) {
	if f.routine == "" {
		return memory.Global, nil
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return "", err
	}
	if _, err := set.Find(f.routine); err != nil {
		return "", err
	}
	return f.routine, nil
}

func scopeName(scope string) string {
	if scope == memory.Global {
		return "every routine"
	}
	return scope
}

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "What routines should remember: mine to add; routines only read them",
		Long: "Memories are plain files in the data folder: memories/_global.md for every routine, " +
			"memories/<routine>.md for one, one memory per \"- \" line (edit them by hand too). A run gets the " +
			"global ones and its routine's through the {{> memories}} partial.",
	}
	cmd.AddCommand(newMemoryListCmd(), newMemoryAddCmd(), newMemoryRmCmd(), newMemoryEditCmd(), newMemoryCompactCmd())
	return cmd
}

func newMemoryListCmd() *cobra.Command {
	var sf scopeFlag
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List memories (all, or one routine's with --routine)",
		Annotations: readOnly(),
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			scopes, err := a.Memories().Scopes()
			if err != nil {
				return err
			}
			if sf.routine != "" {
				scopes = []string{sf.routine}
			}
			all := []memory.Memory{}
			for _, s := range scopes {
				ms, err := a.Memories().List(s)
				if err != nil {
					return err
				}
				all = append(all, ms...)
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), all)
			}
			if len(all) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No memories. Add one: "+product.Command+" memory add \"...\" [--routine <name>]")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tFOR\tMEMORY")
			for _, m := range all {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", m.ID, scopeName(m.Routine), m.Text)
			}
			return tw.Flush()
		}),
	}
	sf.add(cmd, "list")
	jsonFlag(cmd.Flags(), []memory.Memory{})
	return cmd
}

func newMemoryAddCmd() *cobra.Command {
	var sf scopeFlag
	cmd := &cobra.Command{
		Use:     "add <text>",
		Short:   "Remember something (for every routine, or one with --routine)",
		Example: "  " + product.Command + " memory add \"The team's thread is in #eng-updates\" --routine standup",
		Args:    cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			scope, err := sf.scope(a)
			if err != nil {
				return err
			}
			ms, err := a.Memories().List(scope)
			if err != nil {
				return err
			}
			limit, err := memoryLimit(a)
			if err != nil {
				return err
			}
			if size := memory.Size(ms) + len(args[0]); size > limit {
				return fmt.Errorf("that would make %d characters of memories for %s, past memories.max-chars (%d): "+
					"compact them (`"+product.Command+" memory compact%s`) or remove some first",
					size, scopeName(scope), limit, routineArg(scope))
			}
			m, err := a.Memories().Add(scope, args[0])
			if err != nil {
				return err
			}
			return emit(cmd, m, fmt.Sprintf("Remembered for %s (%s).", scopeName(scope), m.ID))
		}),
	}
	sf.add(cmd, "add to")
	jsonFlag(cmd.Flags(), memory.Memory{})
	return cmd
}

func routineArg(scope string) string {
	if scope == memory.Global {
		return ""
	}
	return " --routine " + scope
}

func memoryLimit(a *engine.App) (int, error) {
	vals, err := a.Settings()
	if err != nil {
		return 0, err
	}
	return vals.Int("memories.max-chars"), nil
}

func newMemoryRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Forget a memory",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			m, err := a.Memories().Remove(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Forgot %q.\n", m.Text)
			return nil
		}),
	}
}

func newMemoryEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <id> <text>",
		Short: "Change a memory's text",
		Args:  cobra.ExactArgs(2),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			m, err := a.Memories().Edit(args[0], args[1])
			if err != nil {
				return err
			}
			return emit(cmd, m, fmt.Sprintf("Changed (now %s).", m.ID))
		}),
	}
	jsonFlag(cmd.Flags(), memory.Memory{})
	return cmd
}

// CompactOutput is `memory compact --json`.
type CompactOutput struct {
	Routine string   `json:"routine,omitempty"`
	Before  []string `json:"before"`
	After   []string `json:"after"`
	Applied bool     `json:"applied"`
}

// compactPrompt asks the model to merge a scope's memories.
const compactPrompt = `---
output:
  schema:
    memories(array): string
---
{{role "system"}}
You tidy up a list of things a person asked their automations to remember.
Merge duplicates and near-duplicates into one, and drop any that a later one
contradicts or replaces (later ones are newer). Keep everything else, in the
person's own words; don't add, generalise or explain anything.
{{role "user"}}
{{#each memories}}
- {{this}}
{{/each}}`

func newMemoryCompactCmd() *cobra.Command {
	var sf scopeFlag
	var yes bool
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Merge duplicate and outdated memories, after showing you the result",
		Long: "Asks the model (llm.backend) to merge duplicates and drop memories later ones replaced, without " +
			"adding anything, shows what would change, and asks before saving (--yes without a terminal).",
		Annotations: map[string]string{annNotReadOnly: "memories are mine"},
		Args:        cobra.NoArgs,
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, _ []string) error {
			scope, err := sf.scope(a)
			if err != nil {
				return err
			}
			ms, err := a.Memories().List(scope)
			if err != nil {
				return err
			}
			out := CompactOutput{Routine: scope, Before: []string{}, After: []string{}}
			for _, m := range ms {
				out.Before = append(out.Before, m.Text)
			}
			if len(ms) < 2 {
				out.After = out.Before
				return emit(cmd, out, "Nothing to compact.")
			}
			backend, err := a.NewBackend()
			if err != nil {
				return err
			}
			ag := &agent.Agent{Connector: a.Connector, Backend: backend}
			res, err := ag.Run(cmd.Context(), agent.Request{
				Name: "memory-compact", Source: compactPrompt, Origin: "memory compact",
				Input: map[string]any{"memories": out.Before},
			})
			if err != nil {
				return fmt.Errorf("compacting: %w", err)
			}
			if out.After, err = compacted(res.Output); err != nil {
				return err
			}
			if slices.Equal(out.After, out.Before) {
				return emit(cmd, out, "Nothing to merge or drop.")
			}
			if !wantsJSON(cmd) {
				describeCompact(cmd, out)
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return errors.New("compacting is your decision: run it in a terminal, or add --yes after reviewing the above")
				}
				if !newPrompt(cmd).Confirm("\nSave this?", false) {
					return errors.New("not saved")
				}
			}
			if err := a.Memories().Replace(scope, out.After); err != nil {
				return err
			}
			out.Applied = true
			return emit(cmd, out, fmt.Sprintf("Saved: %d memories, from %d.", len(out.After), len(out.Before)))
		}),
	}
	sf.add(cmd, "compact")
	cmd.Flags().BoolVar(&yes, "yes", false, "save without asking (after reviewing the result)")
	jsonFlag(cmd.Flags(), CompactOutput{})
	return cmd
}

// compacted reads the model's {memories: [...]} result.
func compacted(output any) ([]string, error) {
	m, _ := output.(map[string]any)
	list, ok := m["memories"].([]any)
	if !ok {
		return nil, errors.New("compacting: the model returned no list of memories")
	}
	out := []string{}
	for _, v := range list {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	if len(out) == 0 {
		return nil, errors.New("compacting: the model returned no memories; nothing saved")
	}
	return out, nil
}

func describeCompact(cmd *cobra.Command, out CompactOutput) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Compacting memories for %s: %d → %d\n", scopeName(out.Routine), len(out.Before), len(out.After))
	for _, b := range out.Before {
		if !slices.Contains(out.After, b) {
			fmt.Fprintf(w, "  - %s\n", b)
		}
	}
	for _, a := range out.After {
		if !slices.Contains(out.Before, a) {
			fmt.Fprintf(w, "  + %s\n", a)
		}
	}
}
