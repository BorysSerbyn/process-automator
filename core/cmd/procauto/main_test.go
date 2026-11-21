package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The app's identity is said three times: here (the binary), in install.sh
// (installing a release) and in the Makefile (packaging). They must agree.
func TestIdentityInStep(t *testing.T) {
	install := assignments(t, "../../../install.sh", `^([A-Z_]+)=(.*)$`)
	mk := assignments(t, "../../../Makefile", `^([A-Z_]+)\s*\?=\s*(.*)$`)
	for _, c := range []struct{ what, got, want string }{
		{"install.sh NAME", install["NAME"], app.Name},
		{"install.sh COMMAND", install["COMMAND"], app.Command},
		{"install.sh FOLDER", install["FOLDER"], app.Folder},
		{"install.sh REPO", install["REPO"], app.ReleaseRepo},
		{"install.sh TEAM_ID", install["TEAM_ID"], app.TeamID},
		{"install.sh PUBLIC", install["PUBLIC"], map[bool]string{true: "1"}[app.PublicReleases]},
		{"Makefile BUNDLE_PREFIX", mk["BUNDLE_PREFIX"], app.BundlePrefix},
	} {
		if c.got != c.want {
			t.Errorf("%s is %q, but the app's is %q", c.what, c.got, c.want)
		}
	}
}

// assignments are a file's top-level VAR=value lines, unquoted.
func assignments(t *testing.T, path, pattern string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(pattern)
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			if _, seen := out[m[1]]; !seen {
				out[m[1]] = strings.Trim(strings.TrimSpace(m[2]), `"`)
			}
		}
	}
	return out
}
