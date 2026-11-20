// Command procauto is the Process Automator CLI and backend: the engine
// (internal/). It compiles in no plugins: routines use the core's built-in
// servers and the MCP servers added with `procauto plugins add`.
package main

import (
	"os"

	"github.com/BorysSerbyn/process-automator/core/internal/cli"
)

func main() {
	if err := cli.Main(nil); err != nil {
		os.Exit(1)
	}
}
