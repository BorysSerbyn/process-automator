// Package update moves an install to the latest release by downloading its
// prebuilt artifact. Releases are GitHub releases tagged vMAJOR.MINOR.PATCH,
// with an artifact per platform and a SHA256SUMS file; untagged builds are
// "<next-patch>-dev+<sha>", so a dev build is newer than the release it was
// made after and is left alone.
//
// Installing never runs anything from an artifact before it's verified: its
// checksum, and on macOS its Developer ID signature and notarization (per-OS
// verifyApp). Then the artifact's own installer (install-app.sh, the one
// `make install` and install.sh run) replaces the app in place.
package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// SumsName is the checksums file every release carries (sha256sum format).
const SumsName = "SHA256SUMS"

// InstallerName is the installer script inside an artifact, next to its
// templates (macOS: Contents/Resources; Linux: share/process-automator).
const InstallerName = "install-app.sh"

// maxDownload caps an artifact's size.
const maxDownload = 512 << 20

// Source is where releases come from: a GitHub repository's releases.
type Source struct {
	// API is the GitHub API's base URL (https://api.github.com).
	API string
	// Repo is owner/name.
	Repo string
	// Token authorizes requests (a private repository needs one); empty for none.
	Token  string
	Client *http.Client
}

// Release is a published release.
type Release struct {
	Tag    string  `json:"tag_name"`
	Draft  bool    `json:"draft"`
	Pre    bool    `json:"prerelease"`
	Assets []Asset `json:"assets"`
}

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	// URL is its API URL, which serves the file to an authorized request.
	URL string `json:"url"`
}

// Asset returns the release's file of that name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Version is the release's version without the "v" ("1.2.0").
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func (s Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (s Source) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if s.Token != "" {
		// Go drops it when the download redirects to another host.
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized {
			if s.Token == "" {
				return nil, fmt.Errorf("%s: %s (a private repository needs a GitHub sign-in: `gh auth login`, or set GH_TOKEN)", url, resp.Status)
			}
			return nil, fmt.Errorf("%s: %s (does your GitHub account have access to %s?)", url, resp.Status, s.Repo)
		}
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

// Latest returns the highest published release (no drafts or pre-releases),
// or a zero Release when there are none.
func (s Source) Latest(ctx context.Context) (Release, error) {
	if s.Repo == "" {
		return Release{}, errors.New("this build doesn't know where its releases are")
	}
	resp, err := s.get(ctx, strings.TrimSuffix(s.API, "/")+"/repos/"+s.Repo+"/releases?per_page=50", "application/vnd.github+json")
	if err != nil {
		return Release{}, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var all []Release
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return Release{}, fmt.Errorf("list releases: %w", err)
	}
	var latest Release
	for _, r := range all {
		if r.Draft || r.Pre || !semver.IsValid(r.Tag) || semver.Prerelease(r.Tag) != "" {
			continue
		}
		if latest.Tag == "" || semver.Compare(r.Tag, latest.Tag) > 0 {
			latest = r
		}
	}
	return latest, nil
}

// Newer reports whether the release tag is newer than the running version
// ("1.2.0" or "1.2.1-dev+abc123").
func Newer(current, tag string) bool {
	return tag != "" && semver.Compare(tag, "v"+current) > 0
}

// download saves a release file into dir.
func (s Source) download(ctx context.Context, a Asset, dir string) (string, error) {
	resp, err := s.get(ctx, a.URL, "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("download %s: %w", a.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	path := filepath.Join(dir, filepath.Base(a.Name))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxDownload {
		err = errors.New("larger than expected")
	}
	if err != nil {
		return "", fmt.Errorf("download %s: %w", a.Name, err)
	}
	return path, nil
}

// Fetch downloads the release's artifact of that name into dir and checks it
// against the release's SHA256SUMS, returning its path.
func (s Source) Fetch(ctx context.Context, r Release, name, dir string) (string, error) {
	art, ok := r.Asset(name)
	if !ok {
		return "", fmt.Errorf("release %s has no %s (its build for this platform may have failed)", r.Tag, name)
	}
	sumsAsset, ok := r.Asset(SumsName)
	if !ok {
		return "", fmt.Errorf("release %s has no %s, so its artifact can't be verified", r.Tag, SumsName)
	}
	sums, err := s.download(ctx, sumsAsset, dir)
	if err != nil {
		return "", err
	}
	path, err := s.download(ctx, art, dir)
	if err != nil {
		return "", err
	}
	if err := VerifySum(path, sums); err != nil {
		return "", err
	}
	return path, nil
}

// VerifySum checks the file against its line in a SHA256SUMS file.
func VerifySum(path, sumsPath string) error {
	want, err := sumFor(sumsPath, filepath.Base(path))
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%s: checksum mismatch (got %s, release says %s): not installing it", filepath.Base(path), got, want)
	}
	return nil
}

func sumFor(sumsPath, name string) (string, error) {
	f, err := os.Open(sumsPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s doesn't list %s", SumsName, name)
}

// Install downloads and verifies the release's artifact for this platform,
// then runs its installer to replace the app at appRoot (the installed .app,
// or the installed Linux tree). out gets the installer's output.
func (s Source) Install(ctx context.Context, r Release, appRoot, teamID string, out io.Writer) error {
	name, err := AssetName(r.Version())
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", product.Command+"-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	archive, err := s.Fetch(ctx, r, name, tmp)
	if err != nil {
		return err
	}
	dir := filepath.Join(tmp, "unpacked")
	if err := unpack(archive, dir); err != nil {
		return fmt.Errorf("unpack %s: %w", name, err)
	}
	app, script, err := findInstaller(dir)
	if err != nil {
		return err
	}
	if err := verifyApp(ctx, app, teamID); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "bash", script, "install", "--to", appRoot)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install %s: %w", r.Tag, err)
	}
	return nil
}

// findInstaller finds the installer in an unpacked artifact, and the app it
// installs (two folders above it, in both layouts).
func findInstaller(dir string) (app, script string, err error) {
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == InstallerName {
			script = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if script == "" {
		return "", "", fmt.Errorf("the artifact has no %s", InstallerName)
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(script))), script, nil
}

// Token is a GitHub token for reading releases: GH_TOKEN or GITHUB_TOKEN,
// else the GitHub CLI's sign-in (`gh auth token`), else none (enough for a
// public repository).
func Token(ctx context.Context) string {
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// untarGz unpacks a .tar.gz into dir: regular files and folders only, all inside dir.
func untarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		target := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(h.Mode)&0o755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, io.LimitReader(tr, maxDownload))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("%q: only files and folders are expected", h.Name)
		}
	}
}
