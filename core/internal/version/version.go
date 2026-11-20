// Package version holds build metadata injected at link time.
package version

// Version is set via -ldflags "-X .../internal/version.Version=..." by the Makefile.
var Version = "0.0.0-dev"

// SourceDir is the source checkout this build came from (set by the
// Makefile); development builds without TemplatesDir use its templates/.
var SourceDir = ""

// TemplatesDir is where the bundled templates are, relative to the executable
// (set by the Makefile from how the app is packaged: "../Resources/templates"
// in the macOS .app, "../share/process-automator/templates" in the Linux tree).
// Empty in development builds, which use the source checkout's templates/.
// The installer (install-app.sh) sits next to it.
var TemplatesDir = ""

// ReleaseRepo is the GitHub repository (owner/name) whose releases `update`
// installs, and ReleaseAPI the GitHub API it asks (set by the Makefile from
// install.sh, so the installer and the app agree).
var (
	ReleaseRepo = "BorysSerbyn/process-automator"
	ReleaseAPI  = "https://api.github.com"
)

// TeamID is the Apple Developer team that signs releases (set by the
// Makefile from install.sh). When set, macOS updates must carry that team's
// notarized Developer ID signature.
var TeamID = ""
