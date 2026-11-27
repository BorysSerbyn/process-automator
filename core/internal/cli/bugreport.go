package cli

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// LogsOutput is `routines logs --json` and `hooks logs --json`: the zip of a
// routine's or hook's recent logs, and what's in it, to show before it's
// shared.
type LogsOutput struct {
	// Path is the zip.
	Path string `json:"path"`
	// Name is the routine's or hook's.
	Name string `json:"name"`
	// Hook is true for a hook's logs.
	Hook bool `json:"hook,omitempty"`
	// Runs are the runs in it (history IDs, as `runs show` takes), newest first.
	Runs []string `json:"runs"`
	// Files are the zip's files.
	Files []LogFile `json:"files"`
}

// LogFile is one file in a logs zip.
type LogFile struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

// ReportOutput is `routines report --json` and `hooks report --json`: the
// bug report's new-issue page, filled in, and the logs to attach to it.
type ReportOutput struct {
	// Repo is the GitHub repository (owner/name) the report goes to.
	Repo string `json:"repo"`
	// URL is its new-issue page with the title and body filled in: the
	// reporter files it from their own GitHub account.
	URL   string `json:"url"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Logs is the zip to drag onto the issue (--include-logs): GitHub's
	// new-issue page can't be given a file.
	Logs *LogsOutput `json:"logs,omitempty"`
	// Opened is true when the page was opened in the browser (and the logs
	// shown in the file manager); false with --dry-run or when that failed.
	Opened bool `json:"opened"`
}

// logsDefault is how many runs a logs zip holds unless told otherwise.
const logsDefault = 5

// issueURLMax keeps a new-issue page's address under what browsers and
// GitHub take (about 8 KB): the body loses runs, then text, to fit.
const issueURLMax = 7500

// logOptions are the flags logs and report share: which runs.
type logOptions struct {
	runs  int
	since string
}

func (o *logOptions) flags(cmd *cobra.Command) {
	cmd.Flags().IntVar(&o.runs, "runs", logsDefault, "the latest this many runs")
	cmd.Flags().StringVar(&o.since, "since", "", "only runs started after this: a duration back (7d, 12h) or a date (2026-11-17)")
}

// subject is what logs and report are about: a routine (of either kind) or
// a hook, by name.
type subject struct {
	name string
	hook bool
}

func (s subject) noun() string {
	if s.hook {
		return "hook"
	}
	return "routine"
}

func (s subject) find(a *engine.App) (engine.Runnable, error) {
	if s.hook {
		return a.FindHook(s.name)
	}
	return a.Find(s.name)
}

func newLogsCmd(hook bool) *cobra.Command {
	var o logOptions
	var out string
	what := "routine"
	if hook {
		what = "hook"
	}
	cmd := &cobra.Command{
		Use:         "logs <" + what + ">",
		Annotations: readOnly(),
		Short:       "Export a " + what + "'s recent logs as a zip: its runs, its file and the app's log lines about them, with secrets redacted",
		Long: "Writes a zip of the " + what + "'s latest runs (each as `runs show` prints it, and its record), its file, and " +
			"the scheduler's and frontends' log lines from those runs or naming it. What looks like a token, key or password " +
			"is replaced with [redacted] and the home folder is written ~, but the runs' text and tool results are kept: " +
			"read it before sharing it.",
		Example: "  " + product.Command + " " + what + "s logs standup\n  " + product.Command + " " + what + "s logs standup --since 2d --out ~/Desktop/standup.zip",
		Args:    cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			res, err := exportLogs(a, subject{name: args[0], hook: hook}, o, out, time.Now())
			if err != nil {
				return err
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, res.Path)
			for _, f := range res.Files {
				fmt.Fprintf(w, "  %-28s %s\n", f.Name, byteSize(f.Bytes))
			}
			return nil
		}),
	}
	o.flags(cmd)
	cmd.Flags().StringVar(&out, "out", "", "write the zip here (default: the app's reports folder, kept a week)")
	hideFromMCP(cmd, "out") // a path on my disk isn't a client's business
	jsonFlag(cmd.Flags(), LogsOutput{})
	return cmd
}

func newReportCmd(hook bool) *cobra.Command {
	var o logOptions
	var title, description string
	var includeLogs, dryRun bool
	what := "routine"
	if hook {
		what = "hook"
	}
	cmd := &cobra.Command{
		Use:   "report <" + what + ">",
		Short: "Report a bug in a " + what + ": opens a new issue on the app's GitHub repository, filled in, to file from your own account",
		Long: "Fills in a new issue (the title, what happened, the app's version and OS, the " + what + "'s latest runs) and " +
			"opens it in the browser: you read it and submit it from your GitHub account. With --include-logs, the " + what +
			"'s logs (as `" + what + "s logs` exports them) are saved too and shown in the file manager: drag the zip onto " +
			"the issue (GitHub's page can't be handed a file). Issues on a public repository are public.",
		Example:     "  " + product.Command + " " + what + "s report standup --title \"Posts twice\" --description \"It posted the report twice at 9:20.\" --include-logs",
		Annotations: map[string]string{annReadOnlyWith: "dry-run"},
		Args:        cobra.ExactArgs(1),
		RunE: withApp(func(cmd *cobra.Command, a *engine.App, args []string) error {
			res, err := bugReport(a, subject{name: args[0], hook: hook}, o, title, description, includeLogs, time.Now())
			if err != nil {
				return err
			}
			var problems []string
			if !dryRun {
				res.Opened = true
				if err := openExternal(res.URL); err != nil {
					res.Opened = false
					problems = append(problems, "can't open the browser: "+err.Error())
				}
				if res.Logs != nil {
					if err := revealFile(res.Logs.Path); err != nil {
						problems = append(problems, "can't show the logs: "+err.Error())
					}
				}
			}
			if wantsJSON(cmd) {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			w := cmd.OutOrStdout()
			for _, p := range problems {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", p)
			}
			if res.Opened {
				fmt.Fprintf(w, "Opened a new issue on %s: read it, then submit it.\n", res.Repo)
			} else {
				fmt.Fprintf(w, "Open this to file it on %s:\n%s\n", res.Repo, res.URL)
			}
			if res.Logs != nil {
				fmt.Fprintf(w, "Drag the logs onto the issue before submitting it: %s\n", res.Logs.Path)
			}
			return nil
		}),
	}
	o.flags(cmd)
	cmd.Flags().StringVar(&title, "title", "", "the issue's title (default: \"<name>: \", to finish on the page)")
	cmd.Flags().StringVar(&description, "description", "", "what happened, and what you expected")
	cmd.Flags().BoolVar(&includeLogs, "include-logs", false, "also export the logs to attach (read them first: the issue may be public)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print the page's address (and export the logs): open nothing")
	jsonFlag(cmd.Flags(), ReportOutput{})
	return cmd
}

// bugReport is a report's issue, its page's address and, with includeLogs,
// its logs.
func bugReport(a *engine.App, s subject, o logOptions, title, description string, includeLogs bool, now time.Time) (*ReportOutput, error) {
	repo := product.BugReports
	if repo == "" {
		return nil, fmt.Errorf("%s takes no bug reports (its build names nowhere to send them): export the logs with `%s %ss logs %s` "+
			"and send them to whoever maintains it", product.Name, product.Command, s.noun(), s.name)
	}
	c, err := collect(a, s, o, now)
	if err != nil {
		return nil, err
	}
	res := &ReportOutput{Repo: repo, Title: strings.TrimSpace(title)}
	if res.Title == "" {
		res.Title = s.name + ": "
	}
	if includeLogs {
		if res.Logs, err = c.write(a, ""); err != nil {
			return nil, err
		}
	}
	description = scrub(strings.TrimSpace(description))
	for runs := len(c.runs); ; runs-- {
		res.Body = issueBody(c, description, runs, res.Logs)
		res.URL = issueURL(repo, res.Title, res.Body)
		if len(res.URL) <= issueURLMax || runs == 0 {
			break
		}
	}
	for len(res.URL) > issueURLMax && description != "" { // a description too long to fit: its start
		r := []rune(description)
		description = string(r[:len(r)*3/4]) + "…"
		res.Body = issueBody(c, description, 0, res.Logs)
		res.URL = issueURL(repo, res.Title, res.Body)
	}
	return res, nil
}

func issueURL(repo, title, body string) string {
	q := url.Values{"title": {title}, "body": {body}, "labels": {"bug"}}
	return strings.TrimSuffix(product.ReleaseWeb, "/") + "/" + repo + "/issues/new?" + q.Encode()
}

// issueBody is the issue's text: what happened, where, and the latest runs
// (up to runs of them).
func issueBody(c *collected, description string, runs int, logs *LogsOutput) string {
	var b strings.Builder
	if description == "" {
		description = "<!-- What happened, and what did you expect? -->"
	}
	fmt.Fprintf(&b, "%s\n\n### Where\n\n", description)
	fmt.Fprintf(&b, "- **%s**: `%s`", c.subject.noun(), c.subject.name)
	if c.kind != "" {
		fmt.Fprintf(&b, " (%s)", c.kind)
	}
	fmt.Fprintf(&b, "\n- **app**: %s %s (`%s`), %s/%s\n", product.Name, product.Version, product.Command, routines.Platform(), runtime.GOARCH)
	if c.backend != "" {
		fmt.Fprintf(&b, "- **backend**: %s\n", c.backend)
	}
	if c.problem != "" {
		fmt.Fprintf(&b, "- **problem**: %s\n", oneLine(c.problem, 300))
	}
	if runs > 0 {
		b.WriteString("\n### Latest runs\n\n")
		for _, r := range c.runs[:runs] {
			fmt.Fprintf(&b, "- `%s` (%s): %s", r.ID, r.Trigger, r.Outcome)
			if r.Reason != "" {
				fmt.Fprintf(&b, ": %s", scrub(oneLine(r.Reason, 200)))
			}
			b.WriteString("\n")
		}
	}
	if logs != nil {
		fmt.Fprintf(&b, "\n### Logs\n\n<!-- Drag %s here (it's in %s) before submitting. -->\n", filepath.Base(logs.Path), scrub(filepath.Dir(logs.Path)))
	}
	return b.String()
}

// collected is what a logs zip and a report are made from.
type collected struct {
	subject subject
	now     time.Time
	// kind is the routine's or hook's kind, in words; empty when it doesn't
	// load (problem says why).
	kind, backend, problem string
	source, sourceName     string
	runs                   []history.Summary
	entries                []*history.Entry
}

// collect finds the subject's latest runs and file. One that doesn't load
// (broken, or deleted since its runs) is still collected: its runs are why
// it's reported.
func collect(a *engine.App, s subject, o logOptions, now time.Time) (*collected, error) {
	c := &collected{subject: s, now: now}
	f := history.Filter{Routine: s.name}
	if o.since != "" {
		var err error
		if f.Since, err = parseSince(o.since, now); err != nil {
			return nil, err
		}
	}
	all, err := history.List(a.Layout.History(), f)
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		if r.Hook == s.hook && (o.runs <= 0 || len(c.runs) < o.runs) {
			c.runs = append(c.runs, r)
		}
	}
	x, findErr := s.find(a)
	if findErr != nil && len(c.runs) == 0 {
		return nil, findErr
	}
	if findErr != nil {
		c.problem = findErr.Error()
	} else {
		c.source, c.sourceName = x.Source(), filepath.Base(x.Path())
		c.kind = "LLM " + s.noun()
		if x.Kind() == engine.KindProgram {
			c.kind = "program"
			if s.hook {
				c.kind = "program hook"
			}
		}
	}
	if c.kind != "program" && c.kind != "program hook" {
		if def, err := a.BackendDef(); err == nil {
			c.backend = def.Name
		}
	}
	for _, r := range c.runs {
		e, err := history.Get(a.Layout.History(), r.ID)
		if err != nil {
			return nil, err
		}
		c.entries = append(c.entries, e)
	}
	return c, nil
}

// exportLogs writes a subject's logs zip.
func exportLogs(a *engine.App, s subject, o logOptions, out string, now time.Time) (*LogsOutput, error) {
	c, err := collect(a, s, o, now)
	if err != nil {
		return nil, err
	}
	return c.write(a, out)
}

// write writes the zip to out, or the reports folder.
func (c *collected) write(a *engine.App, out string) (*LogsOutput, error) {
	if out == "" {
		dir := a.Layout.Reports()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		pruneReports(dir, c.now)
		prefix := ""
		if c.subject.hook {
			prefix = "hook-"
		}
		out = filepath.Join(dir, prefix+c.subject.name+"-logs-"+c.now.Format("2006-01-02T150405")+".zip")
	}
	files := c.files(a)
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(f)
	res := &LogsOutput{Path: out, Name: c.subject.name, Hook: c.subject.hook, Runs: []string{}}
	for _, file := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: c.now})
		if err == nil {
			_, err = w.Write([]byte(file.text))
		}
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		res.Files = append(res.Files, LogFile{Name: file.name, Bytes: len(file.text)})
	}
	for _, r := range c.runs {
		res.Runs = append(res.Runs, r.ID)
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return res, f.Close()
}

type namedText struct{ name, text string }

// files are the zip's files, every one scrubbed: about.txt, the subject's
// file, each run (as `runs show` prints it, and its record), and the logs'
// lines about it.
func (c *collected) files(a *engine.App) []namedText {
	var files []namedText
	add := func(name, text string) {
		if text != "" {
			files = append(files, namedText{name, scrub(text)})
		}
	}
	add("about.txt", c.about())
	if c.source != "" {
		add(c.noun()+"/"+c.sourceName, c.source)
	}
	for _, e := range c.entries {
		base := "runs/" + strings.TrimPrefix(e.ID, e.Routine+"/")
		var b strings.Builder
		printRun(&b, e)
		add(base+".txt", b.String())
		if j, err := json.MarshalIndent(e, "", "  "); err == nil {
			add(base+".json", string(j)+"\n")
		}
	}
	since := c.since()
	sched, err := schedulerLog(since)
	if err != nil {
		sched = fmt.Sprintf("(the scheduler's log can't be read: %v)\n", err)
	} else {
		sched = c.logLines(sched, since)
	}
	add("logs/scheduler.log", sched)
	for _, l := range []struct{ name, path string }{
		{"logs/start.log", a.Layout.StartLog()}, {"logs/frontend.log", a.Layout.FrontendLog()},
	} {
		if b, err := os.ReadFile(l.path); err == nil {
			add(l.name, c.logLines(string(b), since))
		} else if !errors.Is(err, fs.ErrNotExist) {
			add(l.name, fmt.Sprintf("(can't be read: %v)\n", err))
		}
	}
	return files
}

func (c *collected) noun() string { return c.subject.noun() }

func (c *collected) about() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s) on %s/%s\n", product.Name, product.Version, product.Command, routines.Platform(), runtime.GOARCH)
	fmt.Fprintf(&b, "%s: %s", c.noun(), c.subject.name)
	if c.kind != "" {
		fmt.Fprintf(&b, " (%s)", c.kind)
	}
	b.WriteString("\n")
	if c.backend != "" {
		fmt.Fprintf(&b, "backend: %s\n", c.backend)
	}
	if c.problem != "" {
		fmt.Fprintf(&b, "problem: %s\n", c.problem)
	}
	fmt.Fprintf(&b, "exported: %s\n", c.now.Format(time.RFC3339))
	fmt.Fprintf(&b, "runs: %d (newest first)\n", len(c.runs))
	for _, r := range c.runs {
		fmt.Fprintf(&b, "  %s  %s  %s\n", r.ID, r.Trigger, r.Outcome)
	}
	b.WriteString("\nWhat looks like a token, key or password is replaced with " + agent.Redacted +
		" and the home folder is written ~. The runs' text and tool results are as they were: read them before sharing.\n")
	return b.String()
}

// logContext is how far around a run its log lines are kept.
const logContext = time.Minute

// logLinesMax caps a log's lines in the zip (the latest are kept).
const logLinesMax = 5000

// since is how far back the logs go: the oldest run's start, else a week.
func (c *collected) since() time.Time {
	if n := len(c.runs); n > 0 {
		return c.runs[n-1].StartedAt.Add(-logContext)
	}
	return c.now.AddDate(0, 0, -7)
}

// logLines keeps a log's lines from during the runs (and a minute around
// them), and those since naming the subject. A line without a time of its
// own (a message's next lines) goes with the one before it.
func (c *collected) logLines(log string, since time.Time) string {
	var kept []string
	keep := false
	sc := bufio.NewScanner(strings.NewReader(log))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if t, ok := lineTime(line); ok {
			keep = !t.Before(since) && (c.duringRun(t) || strings.Contains(line, c.subject.name))
		}
		if keep {
			kept = append(kept, line)
		}
	}
	if len(kept) > logLinesMax {
		kept = append([]string{fmt.Sprintf("(%d earlier lines left out)", len(kept)-logLinesMax)}, kept[len(kept)-logLinesMax:]...)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func (c *collected) duringRun(t time.Time) bool {
	for _, r := range c.runs {
		end := c.now
		if r.FinishedAt != nil {
			end = *r.FinishedAt
		}
		if !t.Before(r.StartedAt.Add(-logContext)) && !t.After(end.Add(logContext)) {
			return true
		}
	}
	return false
}

// lineTime is a log line's time: the scheduler's ("2006-01-02 15:04:05")
// or Go's log package's ("2006/01/02 15:04:05"), local.
func lineTime(line string) (time.Time, bool) {
	if len(line) < 19 {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006/01/02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, line[:19], time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// scrub is text fit for a report: credentials redacted, the home folder ~.
func scrub(s string) string {
	s = agent.Redact(s)
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 {
		s = strings.ReplaceAll(s, home, "~")
	}
	return s
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
