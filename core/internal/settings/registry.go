// Package settings is the single source of truth for user settings: every setting
// is declared once in Registry (key, type, default, constraints, description).
// The settings CLI, validation, the generated contract (contracts/settings.schema.json)
// and future settings UIs are all derived from it. Adding a setting = adding an entry.
package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Type is a setting's value type.
type Type string

// Setting types.
const (
	TypeBool     Type = "bool"
	TypeInt      Type = "int"
	TypeNumber   Type = "number"
	TypePercent  Type = "percent"  // 0–100; accepts "50" or "50%"
	TypeDuration Type = "duration" // Go duration, e.g. "15m", "72h"
	TypeCron     Type = "cron"     // 5-field cron expression, local time
	TypeEnum     Type = "enum"
	TypeString   Type = "string"
	TypePath     Type = "path" // a file or folder; "~/" is the home folder
	TypeList     Type = "list" // a list of strings; on the CLI comma-separated ("a,b"); Enum, if set, limits the items
)

// Types are every setting type.
var Types = []Type{TypeBool, TypeInt, TypeNumber, TypePercent, TypeDuration, TypeCron, TypeEnum, TypeString, TypePath, TypeList}

// Valid reports whether t is a known type.
func (t Type) Valid() bool { return slices.Contains(Types, t) }

// Setting declares one setting.
type Setting struct {
	Key         string   `json:"key"`
	Type        Type     `json:"type"`
	Default     any      `json:"default"`
	Description string   `json:"description"`
	Unit        string   `json:"unit,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	// NotImplemented marks a setting declared ahead of the feature that uses it:
	// it can be set, but has no effect yet.
	NotImplemented bool `json:"notImplemented,omitempty"`
	// Backend is the llm.backend it applies to; empty: every backend.
	Backend string `json:"backend,omitempty"`
}

func ptr(f float64) *float64 { return &f }

// Registry lists every setting. Keys are dotted, lowercase, kebab-case segments.
var Registry = []Setting{
	{
		Key: "llm.backend", Type: TypeEnum, Default: "api", Enum: []string{"api", "claude-code", "codex"},
		Description: "What runs routines: api = Genkit with an Anthropic API key (pay per token); claude-code = headless Claude Code on your Claude subscription; " +
			"codex = headless OpenAI Codex CLI on your Codex sign-in (ChatGPT plan or OpenAI API key).",
	},
	{
		Key: "llm.usage-limit", Type: TypePercent, Default: 90.0, Min: ptr(0), Max: ptr(100), Unit: "%", Backend: "claude-code",
		Description: "Scheduled runs don't start while the Claude plan's 5-hour or weekly usage is above this share " +
			"(they're recorded as skipped); runs started by hand still run. A run that hits the plan's limit stops " +
			"scheduled runs until it resets.",
	},
	{
		Key: "llm.budget.window", Type: TypeDuration, Default: "5h", Backend: "api",
		Description: "Length of the rolling short budget window.",
	},
	{
		Key: "llm.budget.window-usd", Type: TypeNumber, Default: 2.0, Min: ptr(0), Unit: "USD", Backend: "api",
		Description: "API spend allowed in the rolling short window (0: no limit); past it, scheduled runs don't start.",
	},
	{
		Key: "llm.budget.weekly-usd", Type: TypeNumber, Default: 20.0, Min: ptr(0), Unit: "USD", Backend: "api",
		Description: "API spend allowed in the rolling 7 days (0: no limit); past it, scheduled runs don't start.",
	},
	{
		Key: "history.retention-days", Type: TypeInt, Default: 90, Min: ptr(1), Unit: "days",
		Description: "How long run history and logs are kept.",
	},
	{
		Key: "runs.failure-notifications", Type: TypeBool, Default: true,
		Description: "Notify me when a run fails, needs a sign-in, or is skipped or missed (clicking one opens the app on the run).",
	},
	{
		Key: "update.source-dir", Type: TypeString, Default: "",
		Description: "Source checkout `update` builds from when no published release has a download for this machine. " +
			"Empty: the checkout this build came from.",
	},
	{
		Key: "memories.max-chars", Type: TypeInt, Default: 4000, Min: ptr(200), Unit: "characters",
		Description: "How much each memories file may hold (every routine's, and each routine's own): " +
			"what a run's prompt gets, kept small. Past it, `memory add` asks to compact first.",
	},
}

// Retired are settings that are no more: renamed (to their new key) or
// removed (""). A config file an older version wrote reads as if it used the
// new keys, and the next change to the settings rewrites them so.
var Retired = map[string]string{
	"notifications.failures": "runs.failure-notifications",
	// Clicking a notification always shows its run.
	"notifications.on-click": "",
}

// RoutineField is a per-routine setting. A routine file sets it in its
// frontmatter as automator.<Name>; settings override it per routine as
// routines.<routine>.<Name>. Declaring a field here is all it takes: routine
// parsing, validation, `settings`, the settings schema and future settings UIs
// all derive from this list.
type RoutineField struct {
	Name        string
	Type        Type
	Default     any // when the routine file doesn't set it; nil = the file must
	Description string
	Unit        string
	Enum        []string
	Min, Max    *float64
	// ScheduledOnly fields exist only for routines with a schedule.
	ScheduledOnly bool
}

// RoutineFields are every per-routine setting.
var RoutineFields = []RoutineField{
	{
		Name: "enabled", Type: TypeBool, Default: true,
		Description: "Whether the routine runs on its schedule.",
	},
	{
		Name: "schedule", Type: TypeCron, ScheduledOnly: true,
		Description: "When the routine runs (cron, local time). A routine without one runs only by hand.",
	},
	{
		Name: "staleness", Type: TypeDuration, Default: "1h", ScheduledOnly: true,
		Description: "How late a missed run may still run before it's recorded as missed.",
	},
	{
		Name: "timeout", Type: TypeDuration, Default: "20m",
		Description: "How long a run may take before it's stopped and recorded as failed.",
	},
	{
		Name: "effort", Type: TypeEnum, Default: "default",
		Enum:        []string{"default", "low", "medium", "high", "xhigh", "max"},
		Description: "How hard the model thinks; default leaves it to the model backend.",
	},
	{
		Name: "folder", Type: TypePath,
		Description: "Folder the run works in (agent CLI backends), with its project instructions added to the routine's " +
			"(CLAUDE.md on claude-code, AGENTS.md on codex). " +
			"Without one, runs work in an empty temporary folder, or, with session save, in an empty folder of the " +
			"routine's own that's kept.",
	},
	{
		Name: "session", Type: TypeEnum, Default: "discard", Enum: []string{"discard", "save"},
		Description: "save keeps the agent CLI session and records its ID, folder and backend with the run, so it can " +
			"be resumed from that folder with that backend's CLI (the routine's folder, or one of its own when it has " +
			"none); discard leaves nothing behind.",
	},
	{
		Name: "concurrency", Type: TypeEnum, Default: "queue", Enum: []string{"queue", "parallel"},
		Description: "queue waits for other queued runs (one at a time on this device, tightest staleness first); " +
			"parallel runs alongside them. A routine never overlaps itself either way.",
	},
	{
		Name: "builtinTools", Type: TypeList, Default: []string{},
		Description: "Agent CLI built-in tools the run may use, allowed explicitly, in the backend's own names: " +
			"claude-code's tools (e.g. Read, Grep, Bash, Edit); on codex shell, write (the sandbox may write in the " +
			"run's folder), network (with write), web_search, view_image. Dry runs get only those that can't change anything.",
	},
}

// RoutineFieldNames are the automator.* names routine files may use for settings.
func RoutineFieldNames() []string {
	names := make([]string, len(RoutineFields))
	for i, f := range RoutineFields {
		names[i] = f.Name
	}
	return names
}

// RoutineSettings are the settings of one routine, with defaults taken from the
// automator.* values in its file (fileValues; missing ones fall back to the
// field's default). A routine without a schedule gets no scheduled-only fields.
func RoutineSettings(routine string, fileValues map[string]any) []Setting {
	_, scheduled := fileValues["schedule"]
	var out []Setting
	for _, f := range RoutineFields {
		if f.ScheduledOnly && !scheduled {
			continue
		}
		st := Setting{
			Key: "routines." + routine + "." + f.Name, Type: f.Type, Default: f.Default,
			Description: f.Description, Unit: f.Unit, Enum: f.Enum, Min: f.Min, Max: f.Max,
		}
		// The file's value is the routine's default, in canonical form (a YAML
		// list arrives as []any). Routine parsing has already validated it.
		if v, ok := fileValues[f.Name]; ok {
			st.Default = v
			if n, err := st.Normalize(v); err == nil {
				st.Default = n
			}
		}
		out = append(out, st)
	}
	return out
}

// Lookup finds a setting by key in the static registry.
func Lookup(key string) (Setting, bool) { return lookupIn(Registry, key) }

func lookupIn(list []Setting, key string) (Setting, bool) {
	for _, s := range list {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// Parse converts user input (as typed on the CLI) to the setting's canonical value,
// validating it. Canonical values: bool, int, float64 (number/percent), string otherwise.
func (s Setting) Parse(input string) (any, error) {
	in := strings.TrimSpace(input)
	switch s.Type {
	case TypeBool:
		v, err := strconv.ParseBool(in)
		if err != nil {
			return nil, fmt.Errorf("%s: want true or false", s.Key)
		}
		return v, nil
	case TypeInt:
		v, err := strconv.Atoi(in)
		if err != nil {
			return nil, fmt.Errorf("%s: want a whole number", s.Key)
		}
		return v, s.checkRange(float64(v))
	case TypeNumber, TypePercent:
		if s.Type == TypePercent {
			in = strings.TrimSuffix(in, "%")
		}
		v, err := strconv.ParseFloat(in, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: want a number", s.Key)
		}
		return v, s.checkRange(v)
	case TypeDuration:
		d, err := time.ParseDuration(in)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%s: want a positive duration like 15m or 72h", s.Key)
		}
		return in, nil
	case TypeCron:
		if err := checkCron(in); err != nil {
			return nil, fmt.Errorf("%s: %w", s.Key, err)
		}
		return in, nil
	case TypeEnum:
		for _, e := range s.Enum {
			if in == e {
				return in, nil
			}
		}
		return nil, fmt.Errorf("%s: want one of %s", s.Key, strings.Join(s.Enum, ", "))
	case TypeString:
		return in, nil
	case TypePath:
		if in == "" {
			return nil, fmt.Errorf("%s: want a path", s.Key)
		}
		return in, nil
	case TypeList:
		var items []string
		for _, item := range strings.Split(in, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		return s.checkItems(items)
	}
	return nil, fmt.Errorf("%s: unsupported type %s", s.Key, s.Type)
}

// checkItems validates a list's items against Enum (when set).
func (s Setting) checkItems(items []string) ([]string, error) {
	if items == nil {
		items = []string{}
	}
	for _, item := range items {
		if len(s.Enum) > 0 && !slices.Contains(s.Enum, item) {
			return nil, fmt.Errorf("%s: %q isn't one of %s", s.Key, item, strings.Join(s.Enum, ", "))
		}
	}
	return items, nil
}

// Normalize converts a value read from the config file (YAML-decoded) to canonical
// form, validating it, by round-tripping through Parse.
func (s Setting) Normalize(v any) (any, error) {
	if s.Type == TypeList {
		switch list := v.(type) {
		case []string:
			return s.checkItems(list)
		case []any: // as decoded from YAML or JSON
			items := make([]string, 0, len(list))
			for _, item := range list {
				str, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("%s: list items must be text, got %v", s.Key, item)
				}
				items = append(items, str)
			}
			return s.checkItems(items)
		}
	}
	return s.Parse(fmt.Sprint(v))
}

// ExpandPath resolves a path setting's value: "~/" is the home folder.
func ExpandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	return p, nil
}

func (s Setting) checkRange(v float64) error {
	if s.Min != nil && v < *s.Min {
		return fmt.Errorf("%s: must be at least %g", s.Key, *s.Min)
	}
	if s.Max != nil && v > *s.Max {
		return fmt.Errorf("%s: must be at most %g", s.Key, *s.Max)
	}
	return nil
}

// checkCron does a structural check of a 5-field cron expression. The scheduler
// performs full parsing; this catches typos at `settings set` time.
func checkCron(expr string) error {
	if len(strings.Fields(expr)) != 5 {
		return fmt.Errorf("want a 5-field cron expression (minute hour day month weekday), got %q", expr)
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return nil
}

// Text writes a value the way Parse takes it: lists comma-separated, nothing
// for an unset value.
func (s Setting) Text(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case []string:
		return strings.Join(v, ",")
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
