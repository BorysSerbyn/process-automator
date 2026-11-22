// Package automator turns the engine into one app: with its own names,
// identifiers, release source and plugins. cmd/procauto is Process
// Automator; another app imports this package (and pluginapi, for its
// plugins), and its main package calls Main.
//
// What varies from one build to the next (the version, the source checkout,
// where the templates are packaged) is set at link time by the Makefile, with
// -X on this package's unexported variables; an app's main package sets
// nothing at link time.
package automator

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/cli"
	"github.com/BorysSerbyn/process-automator/core/internal/config"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/templates"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// App is what makes a build one app. The Makefile's APP_NAME, CLI, FOLDER and
// BUNDLE_PREFIX, and install.sh's NAME, COMMAND, FOLDER, REPO, TEAM_ID and
// PUBLIC, say the same for packaging and installing: keep them in step.
type App struct {
	// Name is what people see: the app, its windows and its messages
	// ("Process Automator").
	Name string
	// Command is the CLI's name: the binary, and the commands messages
	// suggest ("procauto"). Lowercase letters, digits and dashes.
	Command string
	// Folder names the config and data folder (in the OS's config folder),
	// the logs folder, the release artifacts and the Linux install
	// ("process-automator"). Lowercase letters, digits and dashes.
	Folder string
	// BundlePrefix is the reverse-DNS prefix of the app's identifiers: the
	// bundle ID is BundlePrefix.Command, and the login items and the Keychain
	// service are named after it. A build can override it (BUNDLE_PREFIX).
	BundlePrefix string
	// ReleaseRepo is the GitHub repository (owner/name) whose releases
	// install.sh and `update` install.
	ReleaseRepo string
	// PublicReleases says ReleaseRepo's releases are public: install.sh and
	// `update` read them from GitHub's web pages and downloads, with no
	// sign-in, never running the GitHub CLI or sending a token. Otherwise
	// (false) they're private, read through the GitHub API with a GitHub
	// sign-in (GH_TOKEN or GITHUB_TOKEN, else an account the GitHub CLI is
	// signed in to that can read them). install.sh's PUBLIC says the same.
	PublicReleases bool
	// TeamID is the Apple Developer team that signs releases; when set,
	// installs and updates on macOS accept only its notarized signature.
	TeamID string
	// Plugins are compiled into the app: its own, and any of core/plugins it
	// wants (e.g. notify). The engine adds none.
	Plugins []pluginapi.Plugin
}

// The values the Makefile sets at link time, with -X
// github.com/BorysSerbyn/process-automator/core/automator.<name>=<value>.
// Empty means unset.
var (
	version      string // the build's version (scripts/version.sh)
	sourceDir    string // the source checkout the build came from (SOURCE_DIR)
	templatesDir string // the templates, relative to the executable
	releaseAPI   string // the GitHub API private releases are read from (RELEASE_API)
	releaseWeb   string // the GitHub web address public releases are read from (RELEASE_WEB)
	bundlePrefix string // overrides App.BundlePrefix (BUNDLE_PREFIX)
)

// Main runs the app's command line (os.Args) and returns its error, which
// it has already printed. Call it once, from main.
func Main(app App) error {
	if err := app.validate(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: this build's App:", err)
		return err
	}
	app.apply()
	return cli.Main(app.Plugins)
}

// apply makes this build the app: its values, and the build's set at link
// time over them.
func (a App) apply() {
	product.Name, product.Command, product.Folder = a.Name, a.Command, a.Folder
	product.BundlePrefix = a.BundlePrefix
	product.ReleaseRepo, product.PublicReleases, product.TeamID = a.ReleaseRepo, a.PublicReleases, a.TeamID
	for dst, v := range map[*string]string{
		&product.BundlePrefix: bundlePrefix, &product.Version: version, &product.SourceDir: sourceDir,
		&product.TemplatesDir: templatesDir, &product.ReleaseAPI: releaseAPI, &product.ReleaseWeb: releaseWeb,
	} {
		if v != "" {
			*dst = v
		}
	}
}

func (a App) validate() error {
	var errs []error
	if strings.TrimSpace(a.Name) == "" {
		errs = append(errs, errors.New("no Name"))
	}
	for field, v := range map[string]string{"Command": a.Command, "Folder": a.Folder} {
		if !config.ValidName(v) {
			errs = append(errs, fmt.Errorf("%s %q: use lowercase letters, digits and dashes", field, v))
		}
	}
	if a.BundlePrefix == "" && bundlePrefix == "" {
		errs = append(errs, errors.New("no BundlePrefix"))
	}
	if owner, name, ok := strings.Cut(a.ReleaseRepo, "/"); !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		errs = append(errs, fmt.Errorf("ReleaseRepo %q: expected owner/name", a.ReleaseRepo))
	}
	if err := config.ValidatePlugins(a.Plugins); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// CheckTemplates checks the templates under dir as the app's own are
// checked: each loads as a routine, includes the memories partial
// ({{> memories}}), and renders with the inputs every run gets, using today
// or since. An app's tests run it on the templates it ships; a missing or
// empty folder passes.
func CheckTemplates(dir string) error { return templates.Check(dir) }
