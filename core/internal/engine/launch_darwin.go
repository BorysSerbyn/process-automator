package engine

import (
	"os"
	"strings"
)

// launchableExts are macOS kinds of file that opening would run rather than show.
var launchableExts = []string{".app", ".command", ".tool", ".terminal", ".workflow", ".action", ".pkg", ".mpkg",
	".scpt", ".scptd", ".applescript", ".webloc", ".inetloc", ".fileloc", ".sh", ".py", ".rb", ".pl", ".js"}

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
