// Package update moves an install to the latest release by downloading its
// prebuilt artifact. Releases are GitHub releases tagged vMAJOR.MINOR.PATCH,
// with an artifact per platform and a SHA256SUMS file; untagged builds are
// "<next-patch>-dev+<sha>", so a dev build is newer than the release it was
// made after and is left alone. A public repository's releases are read from
// GitHub's web pages and downloads, with no token; a private one's through
// the GitHub API, signed in.
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
	"net/url"
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

// maxDownload caps an artifact's size, and maxSums a SHA256SUMS file's.
const (
	maxDownload = 512 << 20
	maxSums     = 1 << 20
)

// Source is where releases come from: a GitHub repository's releases, read
// one of two ways. Public ones (Web set) are read from GitHub's web pages and
// downloads, as anyone without an account would: the latest release is the
// one /releases/latest redirects to, and its SHA256SUMS lists its files.
// That needs no token and has no API rate limit, and no token is ever sent.
// Private ones are read through the GitHub API, with a token that can read
// them.
type Source struct {
	// Web is GitHub's web address (https://github.com) when the releases are
	// public; empty when they're read through the API.
	Web string
	// API is the GitHub API's base URL (https://api.github.com), for private
	// releases.
	API string
	// Repo is owner/name.
	Repo string
	// Token authorizes API requests (a private repository needs one); empty
	// for none. It's never sent to Web.
	Token string
	// Accounts are the GitHub CLI accounts Token tried when none could read
	// Repo (for saying so).
	Accounts []string
	Client   *http.Client
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
	// URL serves the file: its API URL (to an authorized request) for a
	// private repository, its download URL for a public one.
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

// public reports whether the releases are read from GitHub's web pages.
func (s Source) public() bool { return s.Web != "" }

// statusError is an answer other than 200 OK.
type statusError struct {
	url, status string
	code        int
}

func (e *statusError) Error() string { return e.url + ": " + e.status }

// notFound reports whether err is a 404 answer.
func notFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code == http.StatusNotFound
}

func (s Source) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if !s.public() {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if s.Token != "" {
			// Go drops it when the download redirects to another host.
			req.Header.Set("Authorization", "Bearer "+s.Token)
		}
	}
	resp, err := s.client().Do(req)
	if err != nil {
		if s.public() {
			return nil, fmt.Errorf("can't reach GitHub: %w", err)
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if !s.public() && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized) {
			if s.Token == "" {
				return nil, fmt.Errorf("%s: %s (a private repository needs a GitHub sign-in: `gh auth login`, or set GH_TOKEN)", url, resp.Status)
			}
			if len(s.Accounts) > 0 {
				return nil, fmt.Errorf("%s: %s (none of the GitHub accounts gh is signed in to can read %s: %s; sign in to one that can with `gh auth login`, or set GH_TOKEN)",
					url, resp.Status, s.Repo, strings.Join(s.Accounts, ", "))
			}
			return nil, fmt.Errorf("%s: %s (does your GitHub account have access to %s?)", url, resp.Status, s.Repo)
		}
		return nil, &statusError{url: url, status: resp.Status, code: resp.StatusCode}
	}
	return resp, nil
}

// Latest returns the highest published release (no drafts or pre-releases),
// or a zero Release when there are none. For public releases it's the one
// GitHub marks as latest: the newest published, which is the highest, since
// releases only move forward (an older one is never installed: see Newer).
func (s Source) Latest(ctx context.Context) (Release, error) {
	if s.Repo == "" {
		return Release{}, errors.New("this build doesn't know where its releases are")
	}
	if s.public() {
		return s.latestPublic(ctx)
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

// releasesURL is the public repository's releases page.
func (s Source) releasesURL() string {
	return strings.TrimSuffix(s.Web, "/") + "/" + s.Repo + "/releases"
}

// downloadURL serves a public release's file.
func (s Source) downloadURL(tag, name string) string {
	return s.releasesURL() + "/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
}

// latestPublic finds the latest release from where GitHub's
// /releases/latest page redirects (…/releases/tag/<tag>, or the releases page
// when there are none), and its files from its SHA256SUMS.
func (s Source) latestPublic(ctx context.Context) (Release, error) {
	latestURL := s.releasesURL() + "/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return Release{}, err
	}
	noFollow := *s.client()
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("can't reach GitHub: %w", err)
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, fmt.Errorf("%s: %s (GitHub has no public repository %s)", latestURL, resp.Status, s.Repo)
	case resp.StatusCode < 300 || resp.StatusCode > 399:
		return Release{}, fmt.Errorf("%s: %s", latestURL, resp.Status)
	}
	loc, err := resp.Location()
	if err != nil {
		return Release{}, fmt.Errorf("%s: %s, to nowhere", latestURL, resp.Status)
	}
	i := strings.LastIndex(loc.Path, "/releases/tag/")
	if i < 0 {
		return Release{}, nil // GitHub sends the releases page: there are none
	}
	tag := loc.Path[i+len("/releases/tag/"):]
	if !semver.IsValid(tag) || semver.Prerelease(tag) != "" {
		return Release{}, fmt.Errorf("the latest release's tag %q isn't a release version (vX.Y.Z)", tag)
	}
	rel := Release{Tag: tag, Assets: []Asset{{Name: SumsName, URL: s.downloadURL(tag, SumsName)}}}
	sums, err := s.get(ctx, rel.Assets[0].URL, "application/octet-stream")
	if notFound(err) {
		return Release{}, fmt.Errorf("release %s has no %s, so its files can't be verified", tag, SumsName)
	}
	if err != nil {
		return Release{}, fmt.Errorf("release %s's %s: %w", tag, SumsName, err)
	}
	defer func() { _ = sums.Body.Close() }()
	sc := bufio.NewScanner(io.LimitReader(sums.Body, maxSums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == SumsName || name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: s.downloadURL(tag, name)})
	}
	if err := sc.Err(); err != nil {
		return Release{}, fmt.Errorf("release %s's %s: %w", tag, SumsName, err)
	}
	return rel, nil
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
// or the installed Linux tree). step is told each step as it starts, in
// words; out gets the installer's output.
func (s Source) Install(ctx context.Context, r Release, appRoot, teamID string, step func(string), out io.Writer) error {
	name, err := AssetName(r.Version())
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", product.Command+"-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	step("Downloading " + r.Tag)
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
	step("Checking " + r.Tag + " is genuine")
	if err := verifyApp(ctx, app, teamID); err != nil {
		return err
	}
	step("Installing " + r.Tag)
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

// gh runs the GitHub CLI and returns what it printed (tests replace it).
var gh = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "gh", args...).Output()
}

// Token is a GitHub token that can read repo's releases: GH_TOKEN or
// GITHUB_TOKEN when set (used as is), else one of the GitHub CLI's sign-ins
// for github.com: the active account if it can read repo, else the first
// other account signed in that can. When none can, it's the active
// account's (or "" when gh isn't signed in: enough for a public repository),
// and accounts lists those tried, for saying so. A token is never printed.
func Token(ctx context.Context, api, repo string) (token string, accounts []string) {
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	active := ghToken(ctx, "")
	logins := ghAccounts(ctx)
	if repo == "" || (active == "" && len(logins) == 0) {
		return active, nil
	}
	probe := Source{API: api, Repo: repo, Client: &http.Client{Timeout: 15 * time.Second}}
	tried := map[string]bool{}
	if active != "" {
		probe.Token = active
		if probe.canRead(ctx) {
			return active, nil
		}
		tried[active] = true
	}
	for _, login := range logins {
		t := ghToken(ctx, login)
		if t == "" || tried[t] {
			continue
		}
		tried[t] = true
		probe.Token = t
		if probe.canRead(ctx) {
			return t, nil
		}
	}
	return active, logins
}

// ghToken is a GitHub CLI account's token for github.com (the active
// account's for ""), or "" when there's none.
func ghToken(ctx context.Context, login string) string {
	args := []string{"auth", "token", "--hostname", "github.com"}
	if login != "" {
		args = append(args, "--user", login)
	}
	out, err := gh(ctx, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ghAccounts are the accounts the GitHub CLI is signed in to for
// github.com, the active one first; none when gh can't say (not
// installed, or too old for `auth status --json`).
func ghAccounts(ctx context.Context) []string {
	out, err := gh(ctx, "auth", "status", "--hostname", "github.com", "--json", "hosts")
	if err != nil {
		return nil
	}
	var status struct {
		Hosts map[string][]struct {
			Login  string `json:"login"`
			Active bool   `json:"active"`
			State  string `json:"state"`
		} `json:"hosts"`
	}
	if json.Unmarshal(out, &status) != nil {
		return nil
	}
	var logins []string
	for _, a := range status.Hosts["github.com"] {
		if a.Login == "" || (a.State != "" && a.State != "success") {
			continue
		}
		if a.Active {
			logins = append([]string{a.Login}, logins...)
		} else {
			logins = append(logins, a.Login)
		}
	}
	return logins
}

// canRead reports whether the token can read the repository.
func (s Source) canRead(ctx context.Context) bool {
	resp, err := s.get(ctx, strings.TrimSuffix(s.API, "/")+"/repos/"+s.Repo, "application/vnd.github+json")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
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
