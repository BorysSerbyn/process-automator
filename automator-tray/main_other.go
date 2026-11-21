//go:build !linux

// automator-tray is the Linux tray of an app built on process-automator
// (macOS has automator-ui).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "automator-tray runs on Linux desktops (macOS has the menu bar app)")
	os.Exit(1)
}
