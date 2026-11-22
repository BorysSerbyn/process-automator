package programs

import (
	"errors"

	"golang.org/x/sys/unix"
)

// quarantineAttr is macOS's mark on a downloaded file, which can make
// Gatekeeper block it from running.
const quarantineAttr = "com.apple.quarantine"

// unquarantine removes the quarantine mark from the app's copy: adding the
// program was my approval, and a mark left on would block scheduled runs.
func unquarantine(path string) error {
	if err := unix.Removexattr(path, quarantineAttr); err != nil && !errors.Is(err, unix.ENOATTR) {
		return err
	}
	return nil
}
