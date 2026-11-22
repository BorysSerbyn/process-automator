package cli

import "strings"

// The permissions' calls and words: what a review's and a window's rows
// show, and Grant…, as automator-ui has them.

// PermissionRequest is `permissions request --json`: how asking the OS went.
type PermissionRequest struct {
	Permission string `json:"permission"`
	Label      string `json:"label"`
	State      string `json:"state"`
	// Asked is whether the scheduler asked the OS; Why says why not.
	Asked       bool   `json:"asked"`
	Why         string `json:"why,omitempty"`
	Where       string `json:"where,omitempty"`
	SettingsURL string `json:"settingsUrl,omitempty"`
}

// RequestPermission has the scheduler ask the OS for a permission, as the
// app (`permissions request`).
func (c CLI) RequestPermission(permission string) (PermissionRequest, error) {
	var out PermissionRequest
	return out, c.call(&out, "permissions", "request", permission)
}

// Blocking is whether it keeps what needs it from running: missing or
// not-checked. Unknown and unsupported never do.
func (p PermissionNeed) Blocking() bool { return p.State == "missing" || p.State == "not-checked" }

// Shown is whether it's shown at all: unknown and unsupported aren't.
func (p PermissionNeed) Shown() bool { return p.State != "unknown" && p.State != "unsupported" }

// Reason is why it's needed, from what declares it: "for its watcher
// home-key", "for the notify plugin", "for its program".
func (p PermissionNeed) Reason() string {
	var parts []string
	for _, s := range p.By {
		switch s.Kind {
		case "watcher":
			parts = append(parts, "its watcher "+s.Name)
		case "plugin":
			parts = append(parts, "the "+s.Name+" plugin")
		default:
			parts = append(parts, "its program")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "for " + strings.Join(parts, " and ")
}

// StateText is its state in words.
func (p PermissionNeed) StateText() string {
	switch p.State {
	case "granted":
		return "Allowed"
	case "missing":
		return "Not allowed"
	case "not-checked":
		return "Not checked yet"
	case "unknown":
		return "Can't be checked"
	}
	return "Not on this computer"
}

// BlockingPermissions are the ones keeping it from running.
func BlockingPermissions(list []PermissionNeed) []PermissionNeed {
	var out []PermissionNeed
	for _, p := range list {
		if p.Blocking() {
			out = append(out, p)
		}
	}
	return out
}

// WontRunText is "It won't run until Input Monitoring and Notifications
// are allowed." ("" when none is missing).
func WontRunText(list []PermissionNeed) string {
	var labels []string
	for _, p := range BlockingPermissions(list) {
		labels = append(labels, p.Label)
	}
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return "It won't run until " + labels[0] + " is allowed."
	}
	return "It won't run until " + strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1] + " are allowed."
}

// Instructions are what to do next while it isn't allowed: "Switch on
// Acme in System Settings > …" ("" once it is).
func (r PermissionRequest) Instructions(app string) string {
	if r.State == "granted" {
		return ""
	}
	verb := "Switch"
	if r.Asked {
		verb = "Answer the prompt, or switch"
	}
	place := ""
	if r.Where != "" {
		place = " " + r.Where
	}
	if app == "" {
		app = "the app"
	}
	return verb + " on " + app + place + "."
}
