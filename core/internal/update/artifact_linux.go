package update

import (
	"context"
	"runtime"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// AssetName is the release artifact for this platform: the core and its
// files as a tarball, per CPU architecture.
func AssetName(version string) (string, error) {
	return product.Folder + "-" + version + "-linux-" + runtime.GOARCH + ".tar.gz", nil
}

func unpack(archive, dir string) error { return untarGz(archive, dir) }

// verifyApp has nothing beyond the checksum to check on Linux.
func verifyApp(context.Context, string, string) error { return nil }
