package engine

import "os/exec"

// openApp opens a .app in the background: it doesn't come to the front
// (tests replace it).
var openApp = func(path string) error { return exec.Command("open", "-g", path).Run() }
