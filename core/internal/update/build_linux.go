package update

import "runtime"

// buildSteps build and install a checkout on Linux: the release tarball for
// this processor, installed by its own installer as a download would be.
func buildSteps(dir string) ([][]string, error) {
	return [][]string{
		{"make", "package-linux", "LINUX_ARCH=" + runtime.GOARCH, "SOURCE_DIR=" + dir},
		{"sh", "-c", `./install.sh --yes --from "$(ls build/dist/*-linux-*.tar.gz | head -n 1)"`},
	}, nil
}
