package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		current, tag string
		want         bool
	}{
		{"1.0.0", "v1.0.1", true},
		{"1.0.0", "v1.0.0", false},
		{"1.0.1", "v1.0.0", false},
		{"1.0.1-dev+abc", "v1.0.0", false}, // dev build made after v1.0.0: left alone
		{"1.0.1-dev+abc", "v1.0.1", true},  // its release came out
		{"0.0.1-dev+abc.dirty", "v1.0.0", true},
		{"1.0.0", "", false}, // no releases yet
	} {
		if got := Newer(tc.current, tc.tag); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.tag, got, tc.want)
		}
	}
}

// fakeGitHub serves releases and their files like the GitHub API: listing
// under /repos/<repo>/releases, files at their API URL, and only with the
// token when one is required.
func fakeGitHub(t *testing.T, token string, releases []Release, files map[string][]byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r) // what GitHub answers for a private repository
			return
		}
		if r.URL.Path == "/repos/o/r/releases" {
			out := make([]Release, len(releases))
			for i, rel := range releases {
				out[i] = rel
				out[i].Assets = nil
				for _, a := range rel.Assets {
					out[i].Assets = append(out[i].Assets, Asset{Name: a.Name, URL: srv.URL + "/assets/" + a.Name})
				}
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/assets/"); ok && r.Header.Get("Accept") == "application/octet-stream" {
			if b, ok := files[name]; ok {
				_, _ = w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestPicksHighestPublishedRelease(t *testing.T) {
	var rels []Release
	for _, tag := range []string{"v1.2.0", "v1.10.0", "v1.9.9", "v2.0.0-rc1", "nightly"} {
		rels = append(rels, Release{Tag: tag})
	}
	rels = append(rels, Release{Tag: "v3.0.0", Draft: true}, Release{Tag: "v2.5.0", Pre: true})
	srv := fakeGitHub(t, "secret", rels, nil)

	got, err := Source{API: srv.URL, Repo: "o/r", Token: "secret"}.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "v1.10.0" {
		t.Fatalf("Latest = %q, want v1.10.0 (semver-ordered, no drafts or pre-releases)", got.Tag)
	}

	_, err = Source{API: srv.URL, Repo: "o/r"}.Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("without a token: %v, want a hint to sign in to GitHub", err)
	}
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestFetchVerifiesTheChecksum(t *testing.T) {
	good := []byte("the artifact")
	rel := Release{Tag: "v1.2.0", Assets: []Asset{{Name: "a.tar.gz"}, {Name: SumsName}}}
	for _, tc := range []struct {
		name, sums string
		wantErr    string
	}{
		{"matches", sha(good) + "  a.tar.gz\n", ""},
		{"binary-mode line", sha(good) + " *a.tar.gz\n", ""},
		{"tampered", sha([]byte("other")) + "  a.tar.gz\n", "checksum mismatch"},
		{"not listed", sha(good) + "  b.tar.gz\n", "doesn't list a.tar.gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeGitHub(t, "", []Release{rel}, map[string][]byte{"a.tar.gz": good, SumsName: []byte(tc.sums)})
			s := Source{API: srv.URL, Repo: "o/r"}
			latest, err := s.Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			path, err := s.Fetch(context.Background(), latest, "a.tar.gz", t.TempDir())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if b, _ := os.ReadFile(path); string(b) != string(good) {
					t.Fatalf("saved %q", b)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}

	srv := fakeGitHub(t, "", []Release{{Tag: "v1.2.0", Assets: []Asset{{Name: "a.tar.gz"}}}}, map[string][]byte{"a.tar.gz": good})
	s := Source{API: srv.URL, Repo: "o/r"}
	latest, _ := s.Latest(context.Background())
	if _, err := s.Fetch(context.Background(), latest, "a.tar.gz", t.TempDir()); err == nil || !strings.Contains(err.Error(), "can't be verified") {
		t.Fatalf("no SHA256SUMS: %v", err)
	}
	if _, err := s.Fetch(context.Background(), latest, "missing.zip", t.TempDir()); err == nil || !strings.Contains(err.Error(), "has no missing.zip") {
		t.Fatalf("no artifact: %v", err)
	}
}

type tarEntry struct {
	name, body string
	typ        byte
	mode       int64
}

func writeTarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body)), Linkname: "/etc/passwd"}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	return path
}

func TestUntarGzAndFindInstaller(t *testing.T) {
	archive := writeTarGz(t, []tarEntry{
		{name: "app/", typ: tar.TypeDir, mode: 0o755},
		{name: "app/bin/procauto", body: "bin", typ: tar.TypeReg, mode: 0o755},
		{name: "app/share/process-automator/install-app.sh", body: "#!/bin/sh", typ: tar.TypeReg, mode: 0o755},
		{name: "app/share/process-automator/templates/x.prompt", body: "x", typ: tar.TypeReg, mode: 0o644},
	})
	dir := t.TempDir()
	if err := untarGz(archive, dir); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "app", "bin", "procauto")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary not unpacked executable: %v %v", fi, err)
	}
	app, script, err := findInstaller(dir)
	if err != nil {
		t.Fatal(err)
	}
	if app != filepath.Join(dir, "app") || script != filepath.Join(dir, "app", "share", "process-automator", InstallerName) {
		t.Fatalf("app %q, script %q", app, script)
	}

	for name, entries := range map[string][]tarEntry{
		"escaping path": {{name: "../evil", body: "x", typ: tar.TypeReg, mode: 0o644}},
		"symlink":       {{name: "app/link", typ: tar.TypeSymlink, mode: 0o777}},
	} {
		if err := untarGz(writeTarGz(t, entries), t.TempDir()); err == nil {
			t.Errorf("%s: unpacked", name)
		}
	}
	if _, _, err := findInstaller(t.TempDir()); err == nil {
		t.Error("an artifact without an installer was accepted")
	}
}

// Without a published release for this machine, the source checkout's
// highest release tag is the latest.
func TestSourceLatest(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	origin, clone := t.TempDir(), filepath.Join(t.TempDir(), "clone")
	run(origin, "init", "-q")
	run(origin, "commit", "-q", "--allow-empty", "-m", "x")
	for _, tag := range []string{"v1.0.0", "v1.10.0", "v1.2.0", "v2.0.0-rc1", "notes"} {
		run(origin, "tag", tag)
	}
	run(origin, "clone", "-q", origin, clone)
	if got, err := SourceLatest(context.Background(), clone); err != nil || got != "v1.10.0" {
		t.Errorf("SourceLatest = %q, %v; want v1.10.0", got, err)
	}
}
