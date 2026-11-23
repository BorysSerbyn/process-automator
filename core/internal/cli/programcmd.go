package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
)

// Programs are routines that run an executable. Adding one, as trusting
// one, is mine: it's done from its description file's path, which a client
// of the MCP server can't give (--from isn't a tool argument, and what comes
// on stdin has no folder to find the executable in).

// addProgram adds the program described by --from as name, or (replace)
// updates the one there; a new one starts paused and untrusted.
func addProgram(cmd *cobra.Command, a *engine.App, name string, src sourceFlags, replace bool) error {
	if src.source != "" || src.from == "" || src.from == "-" {
		return errors.New("a program is added from its description file's path (--from <path>/" + programs.DescriptionFile +
			"), in a terminal: its executable is found beside it")
	}
	p, err := a.AddProgram(name, src.from, replace)
	if err != nil {
		return err
	}
	out := RoutineChange{Routine: p.Name, Kind: engine.KindProgram.Written(), Path: p.Path}
	if !replace {
		off := false
		out.Enabled = &off
		return emit(cmd, out, fmt.Sprintf("Added the program %s (%s), paused and not trusted: it runs only once you "+
			"trust it, dry runs too.\nTurn it on (trust it and run it on its schedule): "+product.Command+" routines trust %s --resume",
			p.Name, p.Executable, p.Name))
	}
	text := fmt.Sprintf("Updated the program %s (%s).", p.Name, p.Executable)
	if x, err := a.Find(name); err == nil {
		if why, err := a.Untrusted(x); err == nil && why != "" {
			text += " It isn't trusted: " + why
		}
	}
	return emit(cmd, out, text)
}

// deleteProgram deletes a program's copy, settings, trust and secrets; its
// run history and data folder are kept.
func deleteProgram(cmd *cobra.Command, a *engine.App, name string) error {
	var keys []string
	if s, ok := a.FindSignIn(name); ok && s.Kind == "program" {
		for _, sec := range s.Secrets {
			keys = append(keys, sec.Key)
		}
	}
	path, err := programs.Delete(a.Layout.Programs(), name)
	if err != nil {
		return err
	}
	if err := (settings.Store{Path: a.Layout.Config()}).ResetPrefix("routines." + name + "."); err != nil {
		return fmt.Errorf("deleted %s, but its settings remain: %w", path, err)
	}
	if err := a.TrustStore().Forget(name); err != nil {
		return fmt.Errorf("deleted %s, but not its trust: %w", path, err)
	}
	for _, k := range keys {
		if err := a.Connector.Secrets.Delete(k); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return fmt.Errorf("deleted %s, but not its secrets: %w", path, err)
		}
	}
	return emit(cmd, RoutineChange{Routine: name, Kind: engine.KindProgram.Written(), Path: path},
		fmt.Sprintf("Deleted the program %s and its settings (its data folder %s is kept).", name, a.Layout.ProgramData(name)))
}
