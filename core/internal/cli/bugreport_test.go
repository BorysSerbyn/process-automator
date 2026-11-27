//go:build unix

package cli

import (
	"archive/zip"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/engine/enginetest"
	"github.com/BorysSerbyn/process-automator/core/internal/history"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
)

// logTime is how the scheduler writes a log line's time.
func logTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") }

// withSchedulerLog makes the scheduler's log text for one test.
func withSchedulerLog(t *testing.T, text string) {
	t.Helper()
	orig := schedulerLog
	schedulerLog = func(time.Time) (string, error) { return text, nil }
	t.Cleanup(func() { schedulerLog = orig })
}

// readZip is a zip's files, by name.
func readZip(t *testing.T, path string) map[string]string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	out := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

// saveRun saves a finished run of name, started at started.
func saveRun(t *testing.T, a *engine.App, name string, hook bool, started time.Time, outcome runstatus.Outcome, reason string) string {
	t.Helper()
	done := started.Add(30 * time.Second)
	if err := history.Save(a.Layout.History(), history.Entry{Routine: name, Hook: hook, Trigger: trigger.Manual, StartedAt: started,
		FinishedAt: &done, Outcome: outcome, Reason: reason}); err != nil {
		t.Fatal(err)
	}
	return history.RunID(name, started)
}

// A routine's logs: its latest runs (a hook's of the same name aren't
// among them), its file, and the log lines from its runs or naming it;
// credentials redacted and the home folder written ~.
func TestRoutineLogs(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue", "other": "queue"}, nil)
	cfg := a.Layout.Config()
	home, _ := os.UserHomeDir()
	now := time.Now().Truncate(time.Second)
	oldest := saveRun(t, a, "standup", false, now.Add(-3*time.Hour), runstatus.OutcomeSuccess, "")
	failed := saveRun(t, a, "standup", false, now.Add(-2*time.Hour), runstatus.OutcomeFailed,
		"slack said invalid_auth for token=xoxb-1234567890-abcdefghij in "+home+"/notes")
	latest := saveRun(t, a, "standup", false, now.Add(-time.Hour), runstatus.OutcomeSuccess, "")
	saveRun(t, a, "standup", true, now.Add(-30*time.Minute), runstatus.OutcomeSuccess, "") // a hook's run

	during := now.Add(-2*time.Hour + 10*time.Second)
	withSchedulerLog(t, strings.Join([]string{
		logTime(now.Add(-4*time.Hour)) + " standup: before the runs asked for", // before the oldest run kept
		logTime(during) + " standup: calling slack_send_message",
		logTime(during.Add(time.Second)) + " other: running alongside it", // during its run: kept
		"  a message's next line",                                        // goes with the line before
		logTime(now.Add(-90*time.Minute)) + " other: on its own",         // neither: dropped
		logTime(now.Add(-80*time.Minute)) + " scheduler: standup is due", // names it: kept
	}, "\n")+"\n")
	if err := os.WriteFile(a.Layout.FrontendLog(), []byte(during.Local().Format("2006/01/02 15:04:05")+" can't open standup's window\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := cliJSON[LogsOutput](t, cfg, "routines", "logs", "standup", "--runs", "2")
	if !slices.Equal(res.Runs, []string{latest, failed}) || res.Hook || res.Name != "standup" {
		t.Fatalf("runs: %+v", res)
	}
	files := readZip(t, res.Path)
	var names []string
	for _, f := range res.Files {
		names = append(names, f.Name)
		if f.Bytes != len(files[f.Name]) {
			t.Errorf("%s: %d bytes listed, %d in the zip", f.Name, f.Bytes, len(files[f.Name]))
		}
	}
	stamp := func(id string) string { return strings.TrimPrefix(id, "standup/") }
	want := []string{"about.txt", "routine/standup.prompt", "runs/" + stamp(latest) + ".txt", "runs/" + stamp(latest) + ".json",
		"runs/" + stamp(failed) + ".txt", "runs/" + stamp(failed) + ".json", "logs/scheduler.log", "logs/frontend.log"}
	if !slices.Equal(names, want) || len(files) != len(want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	if !strings.Contains(files["routine/standup.prompt"], "Go. {{> memories}}") {
		t.Errorf("routine file: %q", files["routine/standup.prompt"])
	}
	all := strings.Join(slices.Collect(func(yield func(string) bool) {
		for _, v := range files {
			if !yield(v) {
				return
			}
		}
	}), "\n")
	if strings.Contains(all, "xoxb-1234567890") || !strings.Contains(files["runs/"+stamp(failed)+".txt"], "[redacted]") {
		t.Errorf("a token isn't redacted:\n%s", files["runs/"+stamp(failed)+".txt"])
	}
	if strings.Contains(all, home) || !strings.Contains(files["runs/"+stamp(failed)+".json"], "~/notes") {
		t.Errorf("the home folder isn't ~:\n%s", files["runs/"+stamp(failed)+".json"])
	}
	sched := files["logs/scheduler.log"]
	for _, keep := range []string{"calling slack_send_message", "running alongside it", "a message's next line", "standup is due"} {
		if !strings.Contains(sched, keep) {
			t.Errorf("scheduler.log lacks %q:\n%s", keep, sched)
		}
	}
	for _, drop := range []string{"before the runs asked for", "on its own"} {
		if strings.Contains(sched, drop) {
			t.Errorf("scheduler.log has %q:\n%s", drop, sched)
		}
	}
	if !strings.Contains(files["logs/frontend.log"], "can't open standup's window") {
		t.Errorf("frontend.log: %q", files["logs/frontend.log"])
	}
	if !strings.Contains(files["about.txt"], "routine: standup (LLM routine)") || !strings.Contains(files["about.txt"], "backend: claude-code") {
		t.Errorf("about.txt:\n%s", files["about.txt"])
	}

	// --out writes it there; --since keeps the runs since then.
	out := filepath.Join(t.TempDir(), "standup.zip")
	res = cliJSON[LogsOutput](t, cfg, "routines", "logs", "standup", "--runs", "0", "--since", "150m", "--out", out)
	if res.Path != out || !slices.Equal(res.Runs, []string{latest, failed}) {
		t.Errorf("--since: %+v", res)
	}
	if res := cliJSON[LogsOutput](t, cfg, "routines", "logs", "standup", "--runs", "0"); !slices.Contains(res.Runs, oldest) {
		t.Errorf("--runs 0 isn't all of them: %v", res.Runs)
	}

	// A routine deleted since its runs still has them; one with neither is unknown.
	if err := os.Remove(filepath.Join(a.Layout.Routines(), "standup.prompt")); err != nil {
		t.Fatal(err)
	}
	res = cliJSON[LogsOutput](t, cfg, "routines", "logs", "standup")
	if files := readZip(t, res.Path); !strings.Contains(files["about.txt"], "problem: ") || files["routine/standup.prompt"] != "" {
		t.Errorf("a deleted routine:\n%s", files["about.txt"])
	}
	if out, err := cli(t, cfg, "routines", "logs", "nobody"); err == nil {
		t.Errorf("logs of no routine: %s", out)
	}
}

// A bug report opens the app's repository's new-issue page, filled in, and
// shows the logs to drag onto it; an app with no repository takes none.
func TestRoutineReport(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"standup": "queue"}, nil)
	cfg := a.Layout.Config()
	now := time.Now().Truncate(time.Second)
	failed := saveRun(t, a, "standup", false, now.Add(-time.Hour), runstatus.OutcomeFailed, "the model gave up")
	orig := product.BugReports
	t.Cleanup(func() { product.BugReports = orig })

	product.BugReports = ""
	if out, err := cli(t, cfg, "routines", "report", "standup", "--dry-run"); err == nil || !strings.Contains(err.Error(), "takes no bug reports") {
		t.Fatalf("no repository: %v %s", err, out)
	}

	product.BugReports = "me/app"
	r := cliJSON[ReportOutput](t, cfg, "routines", "report", "standup", "--dry-run", "--include-logs",
		"--title", "Posts twice", "--description", "It posted twice.")
	u, err := url.Parse(r.URL)
	if err != nil || r.Repo != "me/app" || !strings.HasPrefix(r.URL, product.ReleaseWeb+"/me/app/issues/new?") || r.Opened {
		t.Fatalf("%+v %v", r, err)
	}
	q := u.Query()
	if q.Get("title") != "Posts twice" || q.Get("body") != r.Body || q.Get("labels") != "bug" {
		t.Errorf("the page's fields: %v", q)
	}
	for _, want := range []string{"It posted twice.", "**routine**: `standup` (LLM routine)", "**app**: " + product.Name,
		"`" + failed + "` (manual): failed: the model gave up", "<!-- Drag " + filepath.Base(r.Logs.Path)} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("the body lacks %q:\n%s", want, r.Body)
		}
	}
	if _, err := os.Stat(r.Logs.Path); err != nil {
		t.Errorf("the logs: %v", err)
	}

	// For real: the page opens in the browser and the logs in the file manager.
	var opened, shown []string
	origOpen, origReveal := openExternal, revealFile
	openExternal = func(s string) error { opened = append(opened, s); return nil }
	revealFile = func(s string) error { shown = append(shown, s); return nil }
	t.Cleanup(func() { openExternal, revealFile = origOpen, origReveal })
	r = cliJSON[ReportOutput](t, cfg, "routines", "report", "standup", "--include-logs")
	if !r.Opened || !slices.Equal(opened, []string{r.URL}) || !slices.Equal(shown, []string{r.Logs.Path}) || r.Title != "standup: " {
		t.Errorf("opened %v, shown %v: %+v", opened, shown, r)
	}
	opened = nil
	if r := cliJSON[ReportOutput](t, cfg, "routines", "report", "standup"); r.Logs != nil || len(opened) != 1 {
		t.Errorf("without logs: %+v", r)
	}

	// Too long for a page's address: the description is cut to fit.
	r = cliJSON[ReportOutput](t, cfg, "routines", "report", "standup", "--dry-run", "--description", strings.Repeat("It broke. ", 2000))
	if len(r.URL) > issueURLMax || !strings.Contains(r.Body, "It broke.") {
		t.Errorf("%d bytes: %s", len(r.URL), r.Body)
	}
}

// A hook's logs and report are its own runs, not a routine's of its name.
func TestHookLogs(t *testing.T) {
	a, _ := enginetest.Fixture(t, map[string]string{"brief": "queue"}, nil)
	cfg := a.Layout.Config()
	watcher := hookFiles(t, watchers.DescriptionFile, "description: My calendar\nexecutable: ./watch\nmode: poll\ninterval: 30s\n", "watch", "true\n")
	if out, err := cli(t, cfg, "watchers", "add", "cal", "--from", watcher); err != nil {
		t.Fatalf("watchers add: %v %s", err, out)
	}
	hook := hookFiles(t, hooks.DescriptionFile, "description: Opens Meet\nexecutable: ./act\nwrites: false\non: {watcher: cal, type: meeting.started}\n", "act", "true\n")
	if out, err := cli(t, cfg, "hooks", "add", "meet", "--from", hook); err != nil {
		t.Fatalf("hooks add: %v %s", err, out)
	}
	now := time.Now().Truncate(time.Second)
	run := saveRun(t, a, "meet", true, now.Add(-time.Hour), runstatus.OutcomeFailed, "exit 1")
	saveRun(t, a, "meet", false, now.Add(-2*time.Hour), runstatus.OutcomeSuccess, "") // not the hook's

	res := cliJSON[LogsOutput](t, cfg, "hooks", "logs", "meet")
	if !res.Hook || !slices.Equal(res.Runs, []string{run}) || !strings.HasPrefix(filepath.Base(res.Path), "hook-meet-logs-") {
		t.Fatalf("%+v", res)
	}
	if files := readZip(t, res.Path); !strings.Contains(files["about.txt"], "hook: meet (program hook)") || files["hook/"+hooks.DescriptionFile] == "" {
		t.Errorf("%v\n%s", res.Files, files["about.txt"])
	}
	orig := product.BugReports
	product.BugReports = "me/app"
	t.Cleanup(func() { product.BugReports = orig })
	r := cliJSON[ReportOutput](t, cfg, "hooks", "report", "meet", "--dry-run")
	if !strings.Contains(r.Body, "**hook**: `meet` (program hook)") || strings.Contains(r.Body, "backend") {
		t.Errorf("%s", r.Body)
	}
	if out, err := cli(t, cfg, "routines", "logs", "meet"); err != nil || strings.Contains(out, run) {
		t.Errorf("a routine's logs of a hook's name: %v %s", err, out)
	}
}
