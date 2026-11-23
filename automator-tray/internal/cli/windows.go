package cli

import "time"

// The windows' calls: the commands automator-ui's Setup and Settings
// windows run, with the same arguments.

// Setup is where first-run setup stands (`setup --json`).
func (c CLI) Setup() (SetupOutput, error) {
	var out SetupOutput
	return out, c.call(&out, "setup")
}

// Templates are the routine templates there are (`templates list`).
func (c CLI) Templates() ([]TemplateInfo, error) {
	var out []TemplateInfo
	return out, c.call(&out, "templates", "list")
}

// Value is a template parameter's value, as `templates install --set` takes it.
type Value struct{ Name, Value string }

// InstallTemplate installs a template as a routine, with the values given
// (the others keep the template's defaults).
func (c CLI) InstallTemplate(id string, values []Value) error {
	args := []string{"templates", "install", id}
	for _, v := range values {
		args = append(args, "--set", v.Name+"="+v.Value)
	}
	var out map[string]any
	return c.call(&out, args...)
}

// AppSettings are the app-wide settings (`settings list --scope app`).
func (c CLI) AppSettings() ([]Setting, error) {
	var out []Setting
	return out, c.call(&out, "settings", "list", "--scope", "app")
}

// SetSetting sets a setting from its text (the core validates it).
func (c CLI) SetSetting(key, text string) (Setting, error) {
	var out Setting
	return out, c.call(&out, "settings", "set", key, text)
}

// ResetSetting puts a setting back to its default.
func (c CLI) ResetSetting(key string) (Setting, error) {
	var out Setting
	return out, c.call(&out, "settings", "reset", key)
}

// SignIn signs in where a setup step says (`auth login <server> --browser`):
// OAuth in the browser, keys on a local page there. It waits as long as
// that takes: no time limit.
func (c CLI) SignIn(server string) error {
	_, err := c.exec(0, []string{"auth", "login", server, "--browser"})
	return err
}

// TrustCheck is what a routine can reach, without trusting it (`routines
// trust <routine> --check`).
func (c CLI) TrustCheck(routine string) (TrustOutput, error) {
	var out TrustOutput
	return out, c.call(&out, "routines", "trust", routine, "--check")
}

// TurnOn trusts a routine with what it can reach and runs it on its
// schedule, in one step (`routines trust <routine> --yes --resume`).
func (c CLI) TurnOn(routine string) error {
	var out TrustOutput
	return c.call(&out, "routines", "trust", routine, "--yes", "--resume")
}

// ConnectClaudeCode registers the app's MCP server with Claude Code (`mcp connect`).
func (c CLI) ConnectClaudeCode() error {
	var out map[string]any
	return c.call(&out, "mcp", "connect")
}

// InstallStatusLine has the core record the plan's usage from Claude
// Code's status line (`usage statusline install`).
func (c CLI) InstallStatusLine() error {
	var out map[string]any
	return c.call(&out, "usage", "statusline", "install")
}

// Usage is LLM usage against its limit, for the backend in use (`usage`).
func (c CLI) Usage() (Report, error) {
	var out Report
	return out, c.call(&out, "usage")
}

// LoginItems says whether the app starts at login (`login-items status`).
func (c CLI) LoginItems() (LoginItems, error) {
	var out LoginItems
	return out, c.call(&out, "login-items", "status")
}

// SetStartAtLogin turns starting at login on or off (`login-items enable|disable`).
func (c CLI) SetStartAtLogin(on bool) (LoginItems, error) {
	verb := "disable"
	if on {
		verb = "enable"
	}
	var out LoginItems
	return out, c.call(&out, "login-items", verb)
}

// SetupOutput is `setup --json`: every step, in the core's order.
type SetupOutput struct {
	Steps     []SetupStep `json:"steps"`
	Done      bool        `json:"done"`
	Remaining int         `json:"remaining"`
}

// SetupStep is one thing first-run setup covers, in the core's words.
type SetupStep struct {
	// Step is its kind: templates, backend, usage, mcp, routine, sign-in or turn-on.
	Step     string   `json:"step"`
	Subject  string   `json:"subject,omitempty"`
	Title    string   `json:"title"`
	Done     bool     `json:"done"`
	Optional bool     `json:"optional,omitempty"`
	Auto     bool     `json:"auto,omitempty"`
	Status   string   `json:"status"`
	Ask      string   `json:"ask,omitempty"`
	Setting  string   `json:"setting,omitempty"`
	Settings []string `json:"settings,omitempty"`
	SignIn   string   `json:"signIn,omitempty"`
	Prepare  string   `json:"prepare,omitempty"`
}

// TemplateInfo is one template (`templates list --json`).
type TemplateInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Project     string  `json:"project,omitempty"`
	Installed   bool    `json:"installed"`
	Params      []Param `json:"params,omitempty"`
}

// Param is a template parameter.
type Param struct {
	Name        string   `json:"name"`
	Required    bool     `json:"required"`
	DefaultText string   `json:"defaultText,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Setting is a setting with its value, as text the way `settings set`
// takes it (`settings list --json` and what `settings set`/`reset` print).
type Setting struct {
	Key         string            `json:"key"`
	Type        string            `json:"type"`
	Description string            `json:"description"`
	ValueText   string            `json:"valueText"`
	DefaultText string            `json:"defaultText"`
	IsDefault   bool              `json:"isDefault"`
	Enum        []string          `json:"enum,omitempty"`
	EnumHelp    map[string]string `json:"enumHelp,omitempty"`
	Unit        string            `json:"unit,omitempty"`
	// Inactive says why it has no effect now (another backend's), in the core's words.
	Inactive       string `json:"inactive,omitempty"`
	NotImplemented bool   `json:"notImplemented,omitempty"`
}

// TrustOutput is `routines trust --check --json`: what a routine can reach.
type TrustOutput struct {
	Routine string  `json:"routine"`
	Surface Surface `json:"surface"`
	// Program is a program's: what runs, in place of servers and tools.
	Program *ProgramInfo  `json:"program,omitempty"`
	Changes []string      `json:"changes"`
	Trusted bool          `json:"trusted"`
	Tools   []CheckedTool `json:"tools,omitempty"`
	// ToolsError says why its tools couldn't be checked.
	ToolsError string `json:"toolsError,omitempty"`
	// Hook is true for a hook: Trigger is what it acts on (trusting it
	// covers that too), Watcher whether its watcher is trusted, and
	// Warnings what to weigh first.
	Hook     bool          `json:"hook,omitempty"`
	Trigger  *Trigger      `json:"trigger,omitempty"`
	Watcher  *WatcherTrust `json:"watcher,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	// SignIns are the sign-ins it still needs before it works (`auth login <name>`).
	SignIns []string `json:"signIns,omitempty"`
	// Permissions are what the OS must allow it before it works, each with
	// what the scheduler last found (`permissions request <permission>`
	// asks for one).
	Permissions []PermissionNeed `json:"permissions,omitempty"`
}

// PermissionNeed is a permission something needs, what declares it, and
// what the scheduler last found.
type PermissionNeed struct {
	Permission string `json:"permission"`
	// Label is what the OS calls it.
	Label string `json:"label"`
	// State is granted, missing, unknown, unsupported or not-checked; only
	// missing and not-checked keep it from running.
	State string             `json:"state"`
	By    []PermissionSource `json:"by"`
	// Where says where it's turned on, and SettingsURL opens it (absent where
	// there's none).
	Where       string `json:"where,omitempty"`
	SettingsURL string `json:"settingsUrl,omitempty"`
}

// PermissionSource is what declares a permission: a plugin, a program, a
// hook or a watcher.
type PermissionSource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// NeedsTrust is whether confirming its review trusts anything: it isn't
// trusted, or (a hook) its watcher isn't, which trusting the hook trusts too.
func (t TrustOutput) NeedsTrust() bool { return !t.Trusted || t.WatcherToTrust() != nil }

// WatcherToTrust is the hook's watcher that confirming trusts with it (nil: none).
func (t TrustOutput) WatcherToTrust() *WatcherTrust {
	if t.Watcher == nil || t.Watcher.Trusted || t.Watcher.Program == nil {
		return nil
	}
	return t.Watcher
}

// MissingWatcher is the hook's watcher that can't be trusted: there's no
// such watcher (nil: there's one).
func (t TrustOutput) MissingWatcher() *WatcherTrust {
	if t.Watcher == nil || t.Watcher.Trusted || t.Watcher.Program != nil {
		return nil
	}
	return t.Watcher
}

// ConfirmTitle is the review's one button.
func (t TrustOutput) ConfirmTitle() string {
	if t.NeedsTrust() {
		return "Trust and Turn On"
	}
	return "Turn On"
}

// Trigger is what a hook is trusted to act on.
type Trigger struct {
	Watcher string `json:"watcher"`
	Type    string `json:"type,omitempty"`
	Batch   bool   `json:"batch,omitempty"`
}

// WatcherTrust is a hook's watcher in the hook's review: whether it's
// trusted, and what trusting it approves (absent without a watcher).
type WatcherTrust struct {
	Name        string         `json:"name"`
	Trusted     bool           `json:"trusted"`
	Untrusted   string         `json:"untrusted,omitempty"`
	Description string         `json:"description,omitempty"`
	Mode        string         `json:"mode,omitempty"`
	Program     *ProgramInfo   `json:"program,omitempty"`
	Events      []WatcherEvent `json:"events,omitempty"`
	Changes     []string       `json:"changes,omitempty"`
}

// Surface is what a routine can reach.
type Surface struct {
	Servers      []string `json:"servers"`
	Tools        []string `json:"tools"`
	BuiltinTools []string `json:"builtinTools,omitempty"`
	Folder       string   `json:"folder,omitempty"`
}

// CheckedTool is one of the routine's tools, and whether it only reads.
type CheckedTool struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
}

// Report is `usage --json`: LLM usage against its limit.
type Report struct {
	// Kind is plan, budget or none.
	Kind         string        `json:"kind"`
	Windows      []UsageWindow `json:"windows"`
	Over         bool          `json:"over"`
	Reason       string        `json:"reason,omitempty"`
	LimitPercent *float64      `json:"limitPercent,omitempty"`
	Models       []ModelUsage  `json:"models,omitempty"`
	FetchedAt    *time.Time    `json:"fetchedAt,omitempty"`
	RecordedAt   *time.Time    `json:"recordedAt,omitempty"`
	ReadError    string        `json:"readError,omitempty"`
}

// UsageWindow is usage in one window ("session", "weekly"; "window", "week").
type UsageWindow struct {
	Name        string     `json:"name"`
	Length      string     `json:"length"`
	Over        bool       `json:"over"`
	UsedPercent *float64   `json:"usedPercent,omitempty"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	SpentUSD    *float64   `json:"spentUsd,omitempty"`
	BudgetUSD   *float64   `json:"budgetUsd,omitempty"`
}

// ModelUsage is one model's weekly limit (shown, not applied).
type ModelUsage struct {
	Model       string    `json:"model"`
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// LoginItems is `login-items --json`.
type LoginItems struct {
	Installed bool `json:"installed"`
	Enabled   bool `json:"enabled"`
}
