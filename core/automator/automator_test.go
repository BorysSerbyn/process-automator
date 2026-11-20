package automator

import (
	"strings"
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

var good = App{Name: "Acme Automator", Command: "acme-auto", Folder: "acme-auto", BundlePrefix: "com.acme", ReleaseRepo: "acme/automator"}

func TestValidate(t *testing.T) {
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(a *App){
		"no name":        func(a *App) { a.Name = " " },
		"bad command":    func(a *App) { a.Command = "Acme Auto" },
		"bad folder":     func(a *App) { a.Folder = "" },
		"no prefix":      func(a *App) { a.BundlePrefix = "" },
		"bad repo":       func(a *App) { a.ReleaseRepo = "automator" },
		"nested repo":    func(a *App) { a.ReleaseRepo = "acme/x/y" },
		"invalid plugin": func(a *App) { a.Plugins = []pluginapi.Plugin{{Name: "x"}} },
	} {
		a := good
		change(&a)
		if err := a.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The app's values become the build's, and what the build set at link time
// goes over them.
func TestApply(t *testing.T) {
	saved := []*string{&product.Name, &product.Command, &product.Folder, &product.BundlePrefix, &product.ReleaseRepo,
		&product.TeamID, &product.Version, &product.SourceDir, &product.TemplatesDir, &product.ReleaseAPI, &bundlePrefix, &version}
	before := make([]string, len(saved))
	for i, p := range saved {
		before[i] = *p
	}
	t.Cleanup(func() {
		for i, p := range saved {
			*p = before[i]
		}
	})

	bundlePrefix, version = "ci.test", "1.2.3"
	good.apply()
	got := strings.Join([]string{product.Name, product.Command, product.Folder, product.BundleID(), product.KeychainService(),
		product.ReleaseRepo, product.Version}, ",")
	want := "Acme Automator,acme-auto,acme-auto,ci.test.acme-auto,ci.test.acme-auto,acme/automator,1.2.3"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// An app checks its templates with CheckTemplates: a missing folder has none.
func TestCheckTemplates(t *testing.T) {
	if err := CheckTemplates(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
