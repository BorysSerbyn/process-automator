package cli

// Export Logs… and Report a Bug…: a routine's or a hook's window runs the
// commands automator-ui's run, with the same arguments.

// LogsOutput is `routines logs --json` and `hooks logs --json`: the zip of
// a routine's or hook's recent logs (secrets redacted), in the app's
// reports folder, and what's in it.
type LogsOutput struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Hook bool   `json:"hook,omitempty"`
	// Runs are the runs in it (history IDs), newest first.
	Runs  []string  `json:"runs"`
	Files []LogFile `json:"files"`
}

// LogFile is one file in a logs zip.
type LogFile struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

// ReportOutput is `routines report --json` and `hooks report --json`: the
// bug report's new-issue page on GitHub, filled in, and the logs to drag
// onto it.
type ReportOutput struct {
	// Repo is the GitHub repository (owner/name) it goes to.
	Repo string `json:"repo"`
	// URL is its new-issue page, filed from the reporter's own account.
	URL   string `json:"url"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Logs is the zip to drag onto the issue (GitHub's page can't be
	// handed a file).
	Logs *LogsOutput `json:"logs,omitempty"`
	// Opened is true when the core opened the page in the browser (and the
	// logs in the file manager).
	Opened bool `json:"opened"`
}

// group is the commands' group: routines, or hooks.
func group(hook bool) string {
	if hook {
		return "hooks"
	}
	return "routines"
}

// ExportLogs writes a zip of a routine's (or a hook's) latest runs, its
// file and the app's log lines about them, secrets redacted, into the
// app's reports folder (`routines logs`, `hooks logs`).
func (c CLI) ExportLogs(name string, hook bool) (LogsOutput, error) {
	var out LogsOutput
	return out, c.call(&out, group(hook), "logs", name)
}

// ReportBug fills in a new issue about a routine (or a hook) on the app's
// GitHub repository and has the core open it in the browser, with its logs
// in the file manager when includeLogs (`routines report`, `hooks
// report`). The title and description go as --flag=value, so text starting
// with "-" isn't read as a flag.
func (c CLI) ReportBug(name string, hook bool, title, description string, includeLogs bool) (ReportOutput, error) {
	args := []string{group(hook), "report", name, "--title=" + title, "--description=" + description}
	if includeLogs {
		args = append(args, "--include-logs")
	}
	var out ReportOutput
	return out, c.call(&out, args...)
}
