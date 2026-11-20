//go:build !darwin && !linux

package update

import "errors"

// buildSteps: building from source isn't supported on this OS.
func buildSteps(string) ([][]string, error) {
	return nil, errors.New("building from source isn't supported on this OS")
}
