package cli

import "time"

// What the tray reads of the core's JSON: only the fields it shows or acts
// on, named as contracts/ names them (TestTypesMatchTheContracts checks it).

// RunStatus is `status --json` and each line of `status --watch`.
type RunStatus struct {
	SchemaVersion int        `json:"schemaVersion"`
	ValidUntil    *time.Time `json:"validUntil,omitempty"`
	// Health is ok, error or none.
	Health    string          `json:"health"`
	Busy      bool            `json:"busy"`
	Summary   string          `json:"summary,omitempty"`
	Reasons   []Reason        `json:"reasons,omitempty"`
	SetupLeft []string        `json:"setupLeft,omitempty"`
	Routines  []RoutineStatus `json:"routines"`
	// Hooks are the hooks, as routines are (absent when there are none): a
	// hook is never among Routines.
	Hooks []RoutineStatus `json:"hooks,omitempty"`
}

// Reason is one thing making health error, in the core's words.
type Reason struct {
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
	// Run is the run it's about, if any (as `runs show` takes it).
	Run string `json:"run,omitempty"`
	// Routine is the routine it's about; a hook's name when Hook is true.
	Routine string `json:"routine,omitempty"`
	Hook    bool   `json:"hook,omitempty"`
}

// RoutineStatus is one routine's entry in the status.
type RoutineStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Health  string `json:"health,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Problem says why it can't be scheduled.
	Problem   string     `json:"problem,omitempty"`
	NextRunAt *time.Time `json:"nextRunAt,omitempty"`
	LastRun   *LastRun   `json:"lastRun,omitempty"`
}

// Routine is a routine's entry in the status (nil: none).
func (s *RunStatus) Routine(name string) *RoutineStatus {
	if s == nil {
		return nil
	}
	for i := range s.Routines {
		if s.Routines[i].Name == name {
			return &s.Routines[i]
		}
	}
	return nil
}

// Hook is a hook's entry in the status (nil: none).
func (s *RunStatus) Hook(name string) *RoutineStatus {
	if s == nil {
		return nil
	}
	for i := range s.Hooks {
		if s.Hooks[i].Name == name {
			return &s.Hooks[i]
		}
	}
	return nil
}

// SupportedSchemaVersion is the run status version the tray reads.
const SupportedSchemaVersion = 1

// Notification is one of the app's own notifications (`notifications list
// --pending --json`).
type Notification struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// ClickOutput is `notifications open --json`: what a click shows.
type ClickOutput struct {
	Notification string `json:"notification"`
	// Action is show-run (Routine's Run) or show-app.
	Action  string `json:"action"`
	Routine string `json:"routine,omitempty"`
	Run     string `json:"run,omitempty"`
	// Hook is true when Routine names a hook.
	Hook bool `json:"hook,omitempty"`
}

// UpdateOutput is `update --check --json` and `update --detach --json`.
type UpdateOutput struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	InProgress      bool   `json:"inProgress"`
	Started         bool   `json:"started,omitempty"`
	Note            string `json:"note,omitempty"`
}

// UpdateProgress is `update progress --json`.
type UpdateProgress struct {
	// State is none, installing, done or failed.
	State     string     `json:"state"`
	Tag       string     `json:"tag,omitempty"`
	Step      string     `json:"step,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
	Log       string     `json:"log,omitempty"`
}

// errorOutput is what a command given --json prints when it fails.
type errorOutput struct {
	Error string `json:"error"`
}

// RunningOutput is `login-items start --json` and `login-items stop --json`.
type RunningOutput struct {
	// Scheduler is running or stopped.
	Scheduler string `json:"scheduler"`
	Changed   bool   `json:"changed"`
}

// Paths is `paths --json`: only what the tray uses.
type Paths struct {
	// FrontendLog is where the tray logs.
	FrontendLog string `json:"frontendLog"`
}
