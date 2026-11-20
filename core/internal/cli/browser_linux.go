package cli

import "os/exec"

// openExternal opens a URL, file or folder with the desktop's default app (tests replace it).
var openExternal = func(target string) error { return exec.Command("xdg-open", target).Run() }
