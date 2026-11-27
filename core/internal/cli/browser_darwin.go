package cli

import "os/exec"

// openExternal opens a URL, file or folder with its default app (tests replace it).
var openExternal = func(target string) error { return exec.Command("open", target).Run() }

// revealFile shows a file selected in the Finder (tests replace it).
var revealFile = func(path string) error { return exec.Command("open", "-R", path).Run() }
