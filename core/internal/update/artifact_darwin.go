package update

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// AssetName is the release artifact for this platform: the macOS app, for
// Apple silicon, zipped with ditto. Intel Macs have none.
func AssetName(version string) (string, error) {
	if runtime.GOARCH != "arm64" {
		return "", fmt.Errorf("releases are for Macs with Apple silicon, not %s", runtime.GOARCH)
	}
	return product.Folder + "-" + version + "-macos.zip", nil
}

// unpack uses ditto, which keeps the app's signature and attributes intact.
func unpack(archive, dir string) error {
	if out, err := exec.Command("/usr/bin/ditto", "-x", "-k", archive, dir).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

var teamLine = regexp.MustCompile(`(?m)^TeamIdentifier=(\S+)$`)

// verifyApp checks the app's code signature (every file in it sealed and
// unchanged). Given the release team ID this build was made with, it must
// also be that team's Developer ID signature, notarized by Apple.
func verifyApp(ctx context.Context, app, teamID string) error {
	if out, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
		return fmt.Errorf("the downloaded app's signature doesn't verify: %s", bytes.TrimSpace(out))
	}
	if teamID == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=2", app).CombinedOutput()
	if err != nil {
		return fmt.Errorf("read the downloaded app's signature: %s", bytes.TrimSpace(out))
	}
	got := "none"
	if m := teamLine.FindSubmatch(out); m != nil {
		got = string(m[1])
	}
	if got != teamID {
		return fmt.Errorf("the downloaded app isn't signed by the expected developer (team %s, expected %s): not installing it", got, teamID)
	}
	if out, err := exec.CommandContext(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", app).CombinedOutput(); err != nil {
		return fmt.Errorf("gatekeeper rejects the downloaded app (not notarized?): %s", bytes.TrimSpace(out))
	}
	return nil
}
