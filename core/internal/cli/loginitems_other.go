//go:build !darwin && !linux

package cli

import (
	"errors"
	"runtime"
)

func loginItems(map[string]string) ([]LoginItem, error) {
	return nil, errors.New("starting at login isn't implemented on " + runtime.GOOS + " yet")
}

func setLoginItems(map[string]string, bool) error {
	return errors.New("starting at login isn't implemented on " + runtime.GOOS + " yet")
}
