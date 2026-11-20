//go:build !darwin && !linux

package update

import (
	"context"
	"errors"
	"runtime"
)

// AssetName: there are no releases for this OS.
func AssetName(string) (string, error) {
	return "", errors.New("there are no prebuilt releases for " + runtime.GOOS)
}

func unpack(archive, dir string) error { return untarGz(archive, dir) }

func verifyApp(context.Context, string, string) error { return nil }
