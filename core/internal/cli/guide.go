package cli

import (
	"embed"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BorysSerbyn/process-automator/core/internal/agent"
	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/hooks"
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/programs"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/settings"
	"github.com/BorysSerbyn/process-automator/core/internal/trigger"
	"github.com/BorysSerbyn/process-automator/core/internal/watchers"
	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// The guide is in the binary, so an installed app and its MCP clients have
// it. Its text is written once, in guide/<topic>.md; whatever the code
// defines (the routine keys, backends, built-in tools, plugin.yaml's
// fields) is generated into it, so it can't drift. The
// examples are files a test runs: the routine is created against the
// example server, which is built and installed as a plugin folder.
//
// In a topic's text, a line `%{table:<id>}` is a generated table,
// `%{file:<path>}` an embedded file as a code block, and `%{<name>}` inline
// is a generated value (guideValues).

//go:embed guide/routines.md guide/plugins.md guide/programs.md guide/hooks.md guide/new-notes.prompt guide/testdata/notes-server/main.go guide/testdata/notes-server/plugin.yaml
var guideFiles embed.FS

// guideTopic is one topic of `guide`.
type guideTopic struct {
	Name, Summary, file string
}

var guideTopics = []guideTopic{
	{Name: "routines", Summary: "Writing a routine file: its keys, inputs, models and tools, how a run ends, and turning it on", file: "routines.md"},
	{Name: "plugins", Summary: "Giving routines new tools: adding an MCP server, or writing one and installing it as a plugin folder", file: "plugins.md"},
	{Name: "programs", Summary: "Writing a program: a routine that runs an executable instead of a model, and its program.yaml", file: "programs.md"},
	{Name: "hooks", Summary: "Writing a hook (a prompt or program run on events) and a watcher (what prints the events), and the event format", file: "hooks.md"},
}

// GuideOutput is `guide --json`: the topics (no topic given), or one topic.
type GuideOutput struct {
	// Topics are the guide's topics, when no topic is given.
	Topics []GuideTopic `json:"topics,omitempty"`
	// Topic is the topic shown, with its title and sections.
	Topic    string         `json:"topic,omitempty"`
	Title    string         `json:"title,omitempty"`
	Sections []GuideSection `json:"sections,omitempty"`
}

// GuideTopic is one of the guide's topics.
type GuideTopic struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// GuideSection is one section of a topic.
type GuideSection struct {
	Title string `json:"title"`
	// Text is the section as plain text (Markdown), its tables included.
	Text string `json:"text"`
	// Tables are the section's generated tables, as data.
	Tables []GuideTable `json:"tables,omitempty"`
}

// GuideTable is a table the guide generates from code.
type GuideTable struct {
	ID      string     `json:"id"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

func newGuideCmd() *cobra.Command {
	names := make([]string, len(guideTopics))
	var list strings.Builder
	for i, t := range guideTopics {
		names[i] = t.Name
		fmt.Fprintf(&list, "\n  %-10s %s", t.Name, t.Summary)
	}
	cmd := &cobra.Command{
		Use:   "guide [<topic>]",
		Short: "How to write a routine and how to add a plugin, for people and agents",
		Long: "Without a topic, lists the topics; with one, prints it. Read `guide routines` before writing a " +
			"routine, `guide plugins` before adding an MCP server, `guide programs` before writing a program and `guide hooks` " +
			"before writing a hook or a watcher. Topics:" + list.String(),
		Example:     "  " + product.Command + " guide routines",
		Annotations: readOnly(),
		ValidArgs:   names,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("give one topic: %s", strings.Join(names, ", "))
			}
			if len(args) == 1 && !slices.Contains(names, args[0]) {
				return fmt.Errorf("no topic %q (topics: %s)", args[0], strings.Join(names, ", "))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				out := GuideOutput{}
				for _, t := range guideTopics {
					out.Topics = append(out.Topics, GuideTopic{Name: t.Name, Summary: t.Summary})
				}
				return emit(cmd, out, "Topics ("+product.Command+" guide <topic>):"+list.String())
			}
			i := slices.Index(names, args[0])
			out, err := renderGuide(guideTopics[i], guideSources{plugins: pluginsFrom(cmd.Context())})
			if err != nil {
				return err
			}
			return emit(cmd, out, guideText(out))
		},
	}
	jsonFlag(cmd.Flags(), GuideOutput{})
	return cmd
}

// guideSources are what generated parts are made from, besides packages:
// the app's plugins.
type guideSources struct {
	plugins []pluginapi.Plugin
}

// renderGuide is a topic with its generated parts filled in.
func renderGuide(t guideTopic, src guideSources) (GuideOutput, error) {
	raw, err := guideFiles.ReadFile("guide/" + t.file)
	if err != nil {
		return GuideOutput{}, err
	}
	out := GuideOutput{Topic: t.Name}
	var cur *GuideSection
	var text []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.TrimSpace(strings.Join(text, "\n"))
			out.Sections = append(out.Sections, *cur)
		}
		text = nil
	}
	values := guideValues()
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "# ") && out.Title == "":
			out.Title = strings.TrimPrefix(line, "# ")
			cur = &GuideSection{Title: out.Title}
			continue
		case strings.HasPrefix(line, "## "):
			flush()
			cur = &GuideSection{Title: strings.TrimPrefix(line, "## ")}
			continue
		case cur == nil:
			return out, fmt.Errorf("guide/%s: text before the title", t.file)
		}
		if id, ok := placeholder(line, "table"); ok {
			tbl, err := src.table(id)
			if err != nil {
				return out, fmt.Errorf("guide/%s: %w", t.file, err)
			}
			cur.Tables = append(cur.Tables, tbl)
			text = append(text, tableText(tbl))
			continue
		}
		if file, ok := placeholder(line, "file"); ok {
			b, err := guideFiles.ReadFile(path.Join("guide", file))
			if err != nil {
				return out, fmt.Errorf("guide/%s: %w", t.file, err)
			}
			lang := strings.TrimPrefix(path.Ext(file), ".")
			if lang == "prompt" {
				lang = "handlebars"
			}
			text = append(text, "```"+lang+"\n"+strings.TrimRight(string(b), "\n")+"\n```")
			continue
		}
		line, err := fillValues(line, values)
		if err != nil {
			return out, fmt.Errorf("guide/%s: %w", t.file, err)
		}
		text = append(text, line)
	}
	flush()
	return out, nil
}

// placeholder reads a line that is only `%{kind:arg}`.
func placeholder(line, kind string) (string, bool) {
	arg, ok := strings.CutPrefix(strings.TrimSpace(line), "%{"+kind+":")
	if !ok || !strings.HasSuffix(arg, "}") {
		return "", false
	}
	return strings.TrimSuffix(arg, "}"), true
}

// fillValues replaces each %{name} in line with its value.
func fillValues(line string, values map[string]string) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(line, "%{")
		if i < 0 {
			b.WriteString(line)
			return b.String(), nil
		}
		j := strings.Index(line[i:], "}")
		if j < 0 {
			return "", fmt.Errorf("unclosed %%{ in %q", line)
		}
		name := line[i+2 : i+j]
		v, ok := values[name]
		if !ok {
			return "", fmt.Errorf("no value %%{%s}", name)
		}
		b.WriteString(line[:i] + v)
		line = line[i+j+1:]
	}
}

// guideValues are the values a topic's text can use inline.
func guideValues() map[string]string {
	types := make([]string, len(settings.Types))
	for i, t := range settings.Types {
		types[i] = string(t)
	}
	return map[string]string{
		"command":        product.Command,
		"outcomeTool":    agent.OutcomeTool,
		"outcomes":       codeList([]string{agent.OutcomeDone, agent.OutcomeNothingToDo, agent.OutcomeFailed}, "or"),
		"callCap":        fmt.Sprint(agent.CallResultCap >> 10),
		"runCap":         fmt.Sprint(agent.RunResultCap >> 10),
		"paramTypes":     codeList(types, "or"),
		"resultEnv":      programs.ResultEnv,
		"triggerEnv":     programs.TriggerEnv,
		"forEnv":         programs.ForEnv,
		"runIDEnv":       programs.RunIDEnv,
		"notifyEnv":      programs.NotifyEnv,
		"notifyLimit":    fmt.Sprint(engine.NotifyLimit),
		"dryRunFlag":     programs.DryRunFlag,
		"programPath":    programs.DefaultPath,
		"outputHalf":     fmt.Sprint(programs.OutputCap / 2 >> 10),
		"cursorEnv":      watchers.CursorEnv,
		"watcherEnv":     watchers.NameEnv,
		"untrustedOpen":  engine.UntrustedOpen,
		"untrustedClose": engine.UntrustedClose,
		"eventBlock":     engine.EventBlockTag,
		"loopFactor":     fmt.Sprint(engine.LoopFactor),
		"maxBackoff":     settings.DurationText(engine.MaxStreamBackoff),
	}
}

// codeList is items in backticks, as a list in words: `a`, `b` or `c`.
func codeList(items []string, last string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "`" + s + "`"
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " " + last + " " + quoted[len(quoted)-1]
}

// table generates the table id.
func (s guideSources) table(id string) (GuideTable, error) {
	switch id {
	case "fields":
		return routineFieldsTable(), nil
	case "plugins":
		return s.pluginsTable(), nil
	case "inputs":
		return inputsTable()
	case "backends":
		return backendsTable()
	case "builtins":
		return builtinsTable()
	case "manifest":
		return manifestTable()
	case "programFields":
		return programFieldsTable(), nil
	case "hookFields":
		return fieldsTable(id, hooks.Fields), nil
	case "hookPromptKeys":
		return fieldsTable(id, hooks.PromptKeys), nil
	case "watcherFields":
		return fieldsTable(id, watchers.Fields), nil
	case "hookSettings":
		return hookSettingsTable(), nil
	case "permissions":
		return permissionsTable(), nil
	}
	return GuideTable{}, fmt.Errorf("no table %q", id)
}

// routineFieldsTable is every automator.* key a routine file takes.
func routineFieldsTable() GuideTable {
	t := GuideTable{ID: "fields", Columns: []string{"Key", "Type", "Default", "Setting", "Meaning"}}
	for _, f := range routines.Fields {
		key := routines.Namespace + "." + f.Name
		if f.Written != "" {
			key = f.Written
		}
		t.Rows = append(t.Rows, []string{key, f.Type, "", "no", f.Description})
	}
	for _, f := range settings.RoutineFields {
		typ := string(f.Type)
		if len(f.Enum) > 0 {
			typ += ": " + strings.Join(f.Enum, ", ")
		}
		def := "none"
		switch v := f.Default.(type) {
		case nil:
		case []string:
			def = "[" + strings.Join(v, ", ") + "]"
		default:
			def = fmt.Sprint(v)
		}
		desc := f.Description
		if f.ScheduledOnly && f.Name != "schedule" {
			desc += " Needs " + routines.Namespace + ".schedule."
		}
		t.Rows = append(t.Rows, []string{routines.Namespace + "." + f.Name, typ, def, "yes", desc})
	}
	return t
}

// programFieldsTable is every key a program's description file takes.
func programFieldsTable() GuideTable {
	t := GuideTable{ID: "programFields", Columns: []string{"Key", "Type", "Meaning"}}
	for _, f := range programs.Fields {
		t.Rows = append(t.Rows, []string{f.Name, f.Type, f.Description})
	}
	return t
}

// fieldsTable is a description file's keys.
func fieldsTable(id string, fields []programs.Field) GuideTable {
	t := GuideTable{ID: id, Columns: []string{"Key", "Type", "Meaning"}}
	for _, f := range fields {
		t.Rows = append(t.Rows, []string{f.Name, f.Type, f.Description})
	}
	return t
}

// hookSettingsTable is a hook's own settings.
func hookSettingsTable() GuideTable {
	t := GuideTable{ID: "hookSettings", Columns: []string{"Key", "Type", "Default", "Meaning"}}
	for _, f := range settings.HookFields {
		t.Rows = append(t.Rows, []string{f.Name, string(f.Type), fmt.Sprint(f.Default), f.Description})
	}
	t.Rows = append(t.Rows, []string{strings.Join(settings.HookActionFields, ", "), "", "",
		"An LLM hook's prompt takes these LLM routine settings too (guide routines)."})
	return t
}

// pluginsTable is the plugins the app has built in.
func (s guideSources) pluginsTable() GuideTable {
	t := GuideTable{ID: "plugins", Columns: []string{"Server", "What it is"}}
	for _, p := range s.plugins {
		t.Rows = append(t.Rows, []string{p.Name, p.Description})
	}
	return t
}

// inputHelp says what each input is, in the order the guide lists them.
// Every input needs one (a test runs the guide).
var inputHelp = [][2]string{
	{"today", "the day it runs for"},
	{"since", "the start of the previous workday (on Monday, Friday)"},
	{"now", "the time it runs for: the scheduled time, or when it was started"},
	{"trigger.kind", "what started it: " + codeList([]string{trigger.Schedule, trigger.Manual, trigger.Backfill}, "or")},
	{"trigger.id", "the occurrence's ID, unique per routine and trigger"},
	{"trigger.at", "the time it runs for, as {{now}} gives it"},
}

// inputsTable is the inputs every routine gets, with examples worked out by
// the code that makes them.
func inputsTable() (GuideTable, error) {
	t := GuideTable{ID: "inputs", Columns: []string{"Input", "Example", "Meaning"}}
	at := time.Date(2026, time.November, 20, 9, 20, 0, 0, time.FixedZone("", 3600))
	in := routines.Inputs(trigger.New(trigger.Schedule, at))
	flat := map[string]string{}
	for k, v := range in {
		if m, ok := v.(map[string]any); ok {
			for sub, sv := range m {
				flat[k+"."+sub] = fmt.Sprint(sv)
			}
			continue
		}
		flat[k] = fmt.Sprint(v)
	}
	for _, h := range inputHelp {
		v, ok := flat[h[0]]
		if !ok {
			return t, fmt.Errorf("inputHelp describes %s, which isn't an input", h[0])
		}
		t.Rows = append(t.Rows, []string{"{{" + h[0] + "}}", v, h[1]})
		delete(flat, h[0])
	}
	for k := range flat {
		return t, fmt.Errorf("the input %s has no description (inputHelp)", k)
	}
	return t, nil
}

// backendsTable is each model backend and the models it runs.
func backendsTable() (GuideTable, error) {
	def, ok := settings.Lookup("llm.backend")
	if !ok {
		return GuideTable{}, fmt.Errorf("no llm.backend setting")
	}
	t := GuideTable{ID: "backends", Columns: []string{"Backend", "Models", "Default model", "Built-in tools", "What it is"}}
	for _, name := range def.Enum {
		b, ok := engine.Backends[name]
		if !ok {
			return t, fmt.Errorf("no backend %q", name)
		}
		models := make([]string, len(b.ModelProviders))
		for i, p := range b.ModelProviders {
			models[i] = p + "/<model>"
		}
		builtins := "none"
		if b.Builtins != nil {
			builtins = "the ones listed below"
			if b.Builtins.Open {
				builtins = "any of its tool names; common ones below"
			}
		}
		t.Rows = append(t.Rows, []string{name, strings.Join(models, ", "), b.DefaultModel, builtins, def.EnumHelp[name]})
	}
	return t, nil
}

// builtinsTable is each agent CLI backend's built-in tools.
func builtinsTable() (GuideTable, error) {
	def, ok := settings.Lookup("llm.backend")
	if !ok {
		return GuideTable{}, fmt.Errorf("no llm.backend setting")
	}
	t := GuideTable{ID: "builtins", Columns: []string{"Tool", "Backend", "Dry runs", "Allows"}}
	for _, name := range def.Enum {
		b := engine.Backends[name]
		if b.Builtins == nil {
			continue
		}
		for _, tool := range b.Builtins.Tools {
			dry := "dropped"
			if tool.ReadOnly {
				dry = "kept"
			}
			t.Rows = append(t.Rows, []string{tool.Name, name, dry, tool.Allows})
		}
	}
	return t, nil
}

// permissionsTable is every permission something can declare.
func permissionsTable() GuideTable {
	t := GuideTable{ID: "permissions", Columns: []string{"Permission", "In System Settings", "Allows"}}
	for _, p := range permissions.All {
		t.Rows = append(t.Rows, []string{p.Name, p.Label, p.Means})
	}
	return t
}

// manifestTable is every plugin.yaml field, from pluginManifest.
func manifestTable() (GuideTable, error) {
	t := GuideTable{ID: "manifest", Columns: []string{"Field", "Type", "Meaning"}}
	typ := reflect.TypeOf(pluginManifest{})
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, help := f.Tag.Get("yaml"), f.Tag.Get("guide")
		if name == "" || help == "" {
			return t, fmt.Errorf("plugin.yaml's %s has no yaml name or guide text", f.Name)
		}
		kind := "text"
		switch f.Type.Kind() {
		case reflect.Slice:
			kind = "list of text"
		case reflect.Map:
			kind = "map of NAME: value"
		}
		t.Rows = append(t.Rows, []string{name, kind, help})
	}
	return t, nil
}

// guideText is a topic as plain text, for a terminal.
func guideText(g GuideOutput) string {
	var b strings.Builder
	b.WriteString("# " + g.Title + "\n")
	for i, s := range g.Sections {
		if i > 0 {
			b.WriteString("\n## " + s.Title)
		}
		b.WriteString("\n" + s.Text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// tableText is a table as a list that reads well raw: one line per row,
// its first column in backticks, the middle ones labelled, the last one
// after a colon.
func tableText(t GuideTable) string {
	if len(t.Rows) == 0 {
		return "- (none)"
	}
	lines := make([]string, len(t.Rows))
	for i, row := range t.Rows {
		var mid []string
		for c := 1; c < len(row)-1; c++ {
			if row[c] != "" {
				mid = append(mid, strings.ToLower(t.Columns[c])+": "+row[c])
			}
		}
		line := "- `" + row[0] + "`"
		if len(mid) > 0 {
			line += " (" + strings.Join(mid, "; ") + ")"
		}
		if len(row) > 1 {
			line += ": " + row[len(row)-1]
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}
