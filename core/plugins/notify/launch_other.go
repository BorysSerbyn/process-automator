//go:build !darwin

package notify

import (
	"os"
	"strings"
)

// launchableExts are kinds of file that opening would run on Linux desktops
// and Windows, rather than show: kept broad, since what opens them varies.
var launchableExts = []string{".desktop", ".appimage", ".sh", ".py", ".rb", ".pl", ".js", ".jar", ".run",
	".exe", ".bat", ".cmd", ".com", ".ps1", ".lnk", ".msi", ".vbs", ".scr", ".url"}

// launches reports whether opening path with its default app would run
// something (an app, a script, an executable) rather than show it.
func launches(path string, info os.FileInfo) bool {
	lower := strings.ToLower(path)
	for _, ext := range launchableExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
