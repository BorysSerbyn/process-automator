package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// Building from the source checkout: what update does when no published
// release has an artifact for this machine (before releases are signed and
// published, or for a platform without one). Releases are then the
// checkout's git tags vMAJOR.MINOR.PATCH.

// SourceLatest fetches tags from the checkout's remote and returns the highest
// release tag (e.g. "v1.2.0"), or "" when there are none.
func SourceLatest(ctx context.Context, dir string) (string, error) {
	if _, err := git(ctx, dir, "fetch", "--tags", "--quiet"); err != nil {
		return "", fmt.Errorf("fetch tags: %w", err)
	}
	out, err := git(ctx, dir, "tag", "--list", "v*.*.*")
	if err != nil {
		return "", err
	}
	latest := ""
	for _, tag := range strings.Fields(out) {
		if semver.IsValid(tag) && semver.Prerelease(tag) == "" && semver.Compare(tag, latest) > 0 {
			latest = tag
		}
	}
	return latest, nil
}

// BuildInstall builds and installs tag (buildSteps, per OS) in a temporary
// git worktree, so the checkout itself (and any work in progress there) is
// never touched. SOURCE_DIR tells the build which checkout future updates use.
func BuildInstall(ctx context.Context, dir, tag string, out io.Writer) error {
	steps, err := buildSteps(dir)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", product.Command+"-update-")
	if err != nil {
		return err
	}
	_ = os.Remove(tmp) // git worktree add wants to create it
	if _, err := git(ctx, dir, "worktree", "add", "--detach", tmp, tag); err != nil {
		return fmt.Errorf("check out %s: %w", tag, err)
	}
	defer func() {
		_, _ = git(context.Background(), dir, "worktree", "remove", "--force", tmp)
	}()
	for _, args := range steps {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = tmp, out, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return string(out), nil
}
