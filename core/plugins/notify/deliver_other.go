//go:build !linux && !(darwin && cgo)

package notify

import (
	"errors"
	"runtime"
)

// deliver isn't supported here: desktop notifications need macOS (built
// with cgo) or Linux.
func deliver(Note) error {
	return errors.New("desktop notifications aren't supported on " + runtime.GOOS)
}
