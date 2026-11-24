package update

import "runtime"

// buildSteps build and install a checkout on Linux: the release tarball for
// this processor, installed by its own installer as a download would be.
// An update doesn't require the newest Claude Code a new install does
// (--without-claude-code): the app runs the one installed, by its version.
func buildSteps(dir string) ([][]string, error) {
	return [][]string{
		{"make", "package-linux", "LINUX_ARCH=" + runtime.GOARCH, "SOURCE_DIR=" + dir},
		{"sh", "-c", `./install.sh --yes --without-claude-code --from "$(ls build/dist/*-linux-*.tar.gz | head -n 1)"`},
	}, nil
}
