// Command procauto is the Process Automator CLI and backend: the engine
// (automator) with no plugins of its own. Routines use the engine's built-in
// servers and the MCP servers added with `procauto plugins add`.
package main

import (
	"os"

	"github.com/BorysSerbyn/process-automator/core/automator"
)

// app is Process Automator. The Makefile and install.sh say the same; a test
// keeps them in step.
var app = automator.App{
	Name:         "Process Automator",
	Command:      "procauto",
	Folder:       "process-automator",
	BundlePrefix: "dev",
	ReleaseRepo:  "BorysSerbyn/process-automator",
}

func main() {
	if err := automator.Main(app); err != nil {
		os.Exit(1)
	}
}
