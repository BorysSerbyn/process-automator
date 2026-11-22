package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// prompt asks me questions where I ran a command (a terminal).
type prompt struct {
	in  *bufio.Reader
	out io.Writer
}

func newPrompt(cmd *cobra.Command) *prompt {
	return &prompt{in: bufio.NewReader(cmd.InOrStdin()), out: cmd.OutOrStdout()}
}

// Ask asks q and returns the answer, trimmed.
func (p *prompt) Ask(q string) string {
	fmt.Fprint(p.out, q+" ")
	line, _ := p.in.ReadString('\n')
	return strings.TrimSpace(line)
}

// Confirm asks a yes-or-no question; anything else answers def.
func (p *prompt) Confirm(q string, def bool) bool {
	hint := " [y/N]"
	if def {
		hint = " [Y/n]"
	}
	switch strings.ToLower(p.Ask(q + hint)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

// Pick asks for one of choices, by number or name: a number picks that
// choice (a number that isn't one is asked again); anything else is the
// answer as typed ("" for Enter).
func (p *prompt) Pick(q string, choices []string) string {
	for {
		answer := p.Ask(q)
		n, err := strconv.Atoi(answer)
		switch {
		case err != nil:
			return answer
		case n >= 1 && n <= len(choices):
			return choices[n-1]
		}
		fmt.Fprintf(p.out, "  %d isn't one of them: a number from 1 to %d, or a name\n", n, len(choices))
	}
}
