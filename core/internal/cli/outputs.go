package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// jsonValue is the --json flag: a bool that also carries the type the
// command prints with it, so the contract generator (contracts/cli/*.schema.json)
// finds every command's output on the command itself.
type jsonValue struct {
	on  *bool
	out any
}

func (v jsonValue) String() string   { return strconv.FormatBool(v.on != nil && *v.on) }
func (v jsonValue) Type() string     { return "bool" }
func (v jsonValue) IsBoolFlag() bool { return true }

func (v jsonValue) Set(s string) error {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	*v.on = b
	return nil
}

// jsonFlag adds --json to fs: the command then prints out's type as JSON
// (see wantsJSON and emit) instead of text. On a parent's persistent flags
// it's every subcommand's output.
func jsonFlag(fs *pflag.FlagSet, out any) *pflag.Flag {
	f := fs.VarPF(jsonValue{on: new(bool), out: out}, "json", "", "machine-readable output")
	f.NoOptDefVal = "true"
	return f
}

// JSONOutputs maps each command that has --json (by path, e.g. "routines
// list") to the Go type it prints with it.
func JSONOutputs(root *cobra.Command) map[string]any {
	out := map[string]any{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := c.Flag("json"); c.Runnable() && f != nil {
			if v, ok := f.Value.(jsonValue); ok {
				out[strings.TrimPrefix(c.CommandPath(), root.Name()+" ")] = v.out
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

// emit prints v as JSON when the command was given --json, else text.
func emit(cmd *cobra.Command, v any, text string) error {
	if wantsJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	fmt.Fprintln(cmd.OutOrStdout(), text)
	return nil
}

// reportChecks prints a check's rows (as JSON with --json, else a line
// each) and fails when any row did, naming how many of what.
func reportChecks[T any](cmd *cobra.Command, rows []T, what string, line func(T) (ok bool, text string)) error {
	failed := 0
	var text []string
	for _, r := range rows {
		ok, l := line(r)
		if !ok {
			failed++
		}
		text = append(text, l)
	}
	if err := emit(cmd, rows, strings.Join(text, "\n")); err != nil {
		return err
	}
	if failed > 0 {
		return reported(fmt.Errorf("%d of %d %s have problems", failed, len(rows), what))
	}
	return nil
}
