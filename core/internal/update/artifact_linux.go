package update

import (
	"context"
	"runtime"
)

// AssetName is the release artifact for this platform: the core and its
// files as a tarball, per CPU architecture.
func AssetName(version string) (string, error) {
	return "process-automator-" + version + "-linux-" + runtime.GOARCH + ".tar.gz", nil
}

func unpack(archive, dir string) error { return untarGz(archive, dir) }

// verifyApp has nothing beyond the checksum to check on Linux.
func verifyApp(context.Context, string, string) error { return nil }
