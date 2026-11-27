// Package product is what makes this build one app: its names, its
// identifiers, where its updates come from, and its build metadata. The
// public automator package sets it once, before anything runs (automator.Main);
// the defaults are Process Automator's, which tests see.
package product

// The app's names.
var (
	// Name is what people see: the app, its windows and its messages.
	Name = "Process Automator"
	// Command is the CLI's name: the binary, and the commands messages suggest.
	Command = "procauto"
	// Folder names the app's config and data folder (in the OS's config
	// folder), its logs folder, its release artifacts and its Linux install.
	Folder = "process-automator"
	// BundlePrefix is the reverse-DNS prefix of the app's identifiers (see
	// BundleID).
	BundlePrefix = "dev"
)

// BundleID is the app's bundle ID: BundlePrefix.Command. The login items
// (LoginItemLabels) and the Keychain service derive from it.
func BundleID() string { return BundlePrefix + "." + Command }

// KeychainService namespaces every secret the app stores (`uninstall
// --purge` clears it).
func KeychainService() string { return BundleID() }

// Version is the build's version (scripts/version.sh).
var Version = "0.0.0-dev"

// SourceDir is the source checkout this build came from; development builds
// without TemplatesDir use its templates/, and `update` builds from it before
// releases are published.
var SourceDir = ""

// TemplatesDir is where the bundled templates are, relative to the executable
// (from how the app is packaged: "../Resources/templates" in the macOS .app,
// "../share/<Folder>/templates" in the Linux tree). Empty in development
// builds, which use the source checkout's templates/. The installer
// (install-app.sh) sits next to it.
var TemplatesDir = ""

// ReleaseRepo is the GitHub repository (owner/name) whose releases `update`
// installs. PublicReleases says they're public: they're read from GitHub's
// web pages and downloads (ReleaseWeb), with no token and never through the
// GitHub CLI. Otherwise they're read through the GitHub API (ReleaseAPI),
// signed in.
var (
	ReleaseRepo    = "BorysSerbyn/process-automator"
	PublicReleases = true
	ReleaseWeb     = "https://github.com"
	ReleaseAPI     = "https://api.github.com"
)

// BugReports is the GitHub repository (owner/name) bug reports are filed
// on: `routines report` and `hooks report` open its new-issue page, filled
// in. Empty: the app takes no bug reports.
var BugReports = ""

// TeamID is the Apple Developer team that signs releases. When set, macOS
// updates must carry that team's notarized Developer ID signature.
var TeamID = ""
