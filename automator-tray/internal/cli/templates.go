package cli

import (
	"encoding/json"
	"fmt"
	"time"
)

// Template updates: what I installed from a template that changed since,
// offered after an update (automator-ui's Template Updates).

// OverrideTimeout bounds `templates override`: it may build a program from
// source, which takes minutes.
var OverrideTimeout = 30 * time.Minute

// TemplateUpdate is one install whose template changed (`templates
// outdated --json`).
type TemplateUpdate struct {
	Name string `json:"name"`
	// Kind is routine, program, hook or watcher.
	Kind string `json:"kind"`
	// Template is the template's ID.
	Template         string `json:"template"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	Version          string `json:"version,omitempty"`
	// Changes is what the template says changed (one line).
	Changes string `json:"changes,omitempty"`
	// Edited is whether my copy changed since it was installed: no, yes, or
	// unknown (installed before installs were recorded).
	Edited string `json:"edited"`
	// Skipped is a change I chose not to take (listed only with --all).
	Skipped bool `json:"skipped,omitempty"`
	// Blocked says why it can't be overridden ("": it can).
	Blocked string `json:"blocked,omitempty"`
	// With are the watchers overridden with a hook.
	With []string `json:"with,omitempty"`
}

// Overridden is one install `templates override` replaced, and whether
// it's trusted now.
type Overridden struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	Trusted bool   `json:"trusted"`
}

// OverrideOutput is `templates override --json`.
type OverrideOutput struct {
	Overridden []Overridden `json:"overridden"`
}

// SkipOutput is `templates skip --json`.
type SkipOutput struct {
	Skipped []string `json:"skipped"`
}

// TemplateUpdates are the installs whose template changed, not counting
// the changes I chose not to take (`templates outdated`): no network.
func (c CLI) TemplateUpdates() ([]TemplateUpdate, error) {
	var out []TemplateUpdate
	return out, c.call(&out, "templates", "outdated")
}

// OverrideTemplates replaces installs with their template's new version
// (`templates override <name>...`); it can take minutes (OverrideTimeout).
// On an error, some may have been overridden already.
func (c CLI) OverrideTemplates(names ...string) (OverrideOutput, error) {
	var out OverrideOutput
	stdout, err := c.exec(OverrideTimeout, append(append([]string{"templates", "override"}, names...), "--json"))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return out, &Failure{Message: fmt.Sprintf("%s answered something unexpected: %v", c.Command, err)}
	}
	return out, nil
}

// SkipTemplateUpdates leaves installs as they are: their template's change
// isn't offered again, a later one is (`templates skip <name>...`).
func (c CLI) SkipTemplateUpdates(names ...string) error {
	var out SkipOutput
	return c.call(&out, append([]string{"templates", "skip"}, names...)...)
}
