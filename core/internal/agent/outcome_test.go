package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/mcp"
)

// script is a fake model: it calls the job's tools in order, then stops.
type script []struct {
	tool string
	args map[string]any
}

func (script) Name() string { return "script" }

func (s script) Run(ctx context.Context, j Job) (*Output, error) {
	if !strings.Contains(j.Instructions, OutcomeTool) {
		return nil, errors.New("backend wasn't told to report the outcome")
	}
	byName := map[string]Tool{}
	for _, t := range j.Tools {
		byName[t.Name] = t
	}
	for _, step := range s {
		t, ok := byName[step.tool]
		if !ok {
			return nil, errors.New("tool not offered: " + step.tool)
		}
		_, _ = t.Call(ctx, step.args) // like a model, carry on after tool errors
	}
	return &Output{Text: "finished"}, nil
}

func call(tool string, args map[string]any) struct {
	tool string
	args map[string]any
} {
	return struct {
		tool string
		args map[string]any
	}{tool, args}
}

func report(outcome string) struct {
	tool string
	args map[string]any
} {
	return call(OutcomeTool, map[string]any{"outcome": outcome, "reason": "because", "evidence": "what showed it"})
}

// reportCase reports nothing_to_do citing kase.
func reportCase(kase string) struct {
	tool string
	args map[string]any
} {
	return call(OutcomeTool, map[string]any{"outcome": OutcomeNothingToDo, "reason": "because", "case": kase, "evidence": "what showed it"})
}

// scrum is the contract of a routine that replies in today's stand-up thread.
var scrum = Contract{
	Done:        "A reply is posted in today's stand-up thread.",
	NothingToDo: []string{"Already replied in today's thread", "No stand-up today (a holiday)"},
	Writes:      []string{"post"},
}

// testAgent serves a "chat" plugin: read (read-only), post (fails while
// failPosts > 0), and expired (a sign-in that fails mid-run).
func testAgent(failPosts int, dryRun bool, s script) *Agent {
	return testAgentReading(failPosts, dryRun, s, "hello")
}

// testAgentReading is testAgent whose read returns readResult.
func testAgentReading(failPosts int, dryRun bool, s script, readResult string) *Agent {
	var mu sync.Mutex // calls can come in parallel
	newServer := func() (*sdk.Server, error) {
		srv := sdk.NewServer(&sdk.Implementation{Name: "chat"}, nil)
		ro := &sdk.ToolAnnotations{ReadOnlyHint: true}
		sdk.AddTool(srv, &sdk.Tool{Name: "read", Annotations: ro}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return textResult(readResult, false), nil, nil
		})
		sdk.AddTool(srv, &sdk.Tool{Name: "post"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			mu.Lock()
			defer mu.Unlock()
			if failPosts > 0 {
				failPosts--
				return textResult("channel is externally shared", true), nil, nil
			}
			return textResult("posted", false), nil, nil
		})
		return srv, nil
	}
	return &Agent{
		Connector: &mcp.Connector{Builtins: map[string]func(mcp.Run) (*sdk.Server, error){"chat": func(mcp.Run) (*sdk.Server, error) { return newServer() }}},
		Backend:   s, DryRun: dryRun,
	}
}

func run(t *testing.T, ag *Agent) (*Result, error) {
	t.Helper()
	return runWith(t, ag, Contract{})
}

func runWith(t *testing.T, ag *Agent, c Contract) (*Result, error) {
	t.Helper()
	return ag.Run(context.Background(), Request{Name: "r", Servers: []string{"chat"}, Tools: []string{"read", "post"}, Contract: c})
}

func TestOutcomes(t *testing.T) {
	for name, tc := range map[string]struct {
		failPosts int
		dryRun    bool
		contract  Contract
		steps     script
		wantErr   string // "" = success
		outcome   string
	}{
		"done":                         {steps: script{call("post", nil), report(OutcomeDone)}, outcome: OutcomeDone},
		"no report":                    {steps: script{call("post", nil)}, wantErr: "without reporting"},
		"reported failed":              {steps: script{report(OutcomeFailed)}, wantErr: "reported it failed"},
		"write refused, model says ok": {failPosts: 1, steps: script{call("post", nil), report(OutcomeDone)}, wantErr: "post failed: channel is externally shared"},
		"write retried and succeeded":  {failPosts: 1, steps: script{call("post", nil), call("post", nil), report(OutcomeDone)}, outcome: OutcomeDone},
		"dry run intercepts writes":    {failPosts: 5, dryRun: true, steps: script{call("post", nil), report(OutcomeDone)}, outcome: OutcomeDone},
		"bad report then good":         {steps: script{call(OutcomeTool, map[string]any{"outcome": "great"}), report(OutcomeDone)}, outcome: OutcomeDone},

		// Without declared cases there's no nothing_to_do: the report is refused.
		"nothing to do, none declared": {steps: script{call("read", nil), report(OutcomeNothingToDo)}, wantErr: "without reporting"},
		"nothing to do, then failed":   {steps: script{call("read", nil), report(OutcomeNothingToDo), report(OutcomeFailed)}, wantErr: "reported it failed"},
		// With a contract: nothing_to_do only citing a declared case.
		"nothing to do, a declared case": {contract: scrum, steps: script{call("read", nil), reportCase(scrum.NothingToDo[1])}, outcome: OutcomeNothingToDo},
		"nothing to do, an undeclared case": {contract: scrum, steps: script{call("read", nil),
			reportCase("No stand-up message in the channel")}, wantErr: "without reporting"},
		"nothing to do, no case": {contract: scrum, steps: script{call("read", nil), report(OutcomeNothingToDo)}, wantErr: "without reporting"},
		// done needs the declared writes to have succeeded.
		"done without the declared write": {contract: scrum, steps: script{call("read", nil), report(OutcomeDone)}, wantErr: "without reporting"},
		"done after the declared write":   {contract: scrum, steps: script{call("post", nil), report(OutcomeDone)}, outcome: OutcomeDone},
		"done after the write failed, then retried": {contract: scrum, failPosts: 1,
			steps: script{call("post", nil), report(OutcomeDone), call("post", nil), report(OutcomeDone)}, outcome: OutcomeDone},
		"dry run: an intercepted write counts": {contract: scrum, dryRun: true, failPosts: 5,
			steps: script{call("post", nil), report(OutcomeDone)}, outcome: OutcomeDone},
		"done without evidence": {contract: scrum, steps: script{call("post", nil),
			call(OutcomeTool, map[string]any{"outcome": OutcomeDone, "reason": "posted"})}, outcome: OutcomeDone},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := runWith(t, testAgent(tc.failPosts, tc.dryRun, tc.steps), tc.contract)
			if tc.wantErr == "" {
				if err != nil || res.Outcome != tc.outcome {
					t.Fatalf("got outcome %q, err %v; want %q", res.Outcome, err, tc.outcome)
				}
				return
			}
			var failed *RoutineFailedError
			if !errors.As(err, &failed) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want a routine failure containing %q", err, tc.wantErr)
			}
		})
	}
}

// The report tool offers what the contract allows: without cases, done and
// failed only; with them, nothing_to_do and an enum of the cases.
func TestOutcomeToolSchema(t *testing.T) {
	props := func(c Contract) map[string]any {
		return outcomeTool(&Result{}, c).InputSchema["properties"].(map[string]any)
	}
	p := props(Contract{})
	if got := p["outcome"].(map[string]any)["enum"]; !slices.Equal(got.([]string), []string{OutcomeDone, OutcomeFailed}) {
		t.Errorf("no contract: outcomes %v", got)
	}
	if _, ok := p["case"]; ok {
		t.Error("no contract: offers a case")
	}
	if _, ok := p["evidence"]; !ok {
		t.Error("no evidence field")
	}
	p = props(scrum)
	if got := p["outcome"].(map[string]any)["enum"]; !slices.Equal(got.([]string), []string{OutcomeDone, OutcomeNothingToDo, OutcomeFailed}) {
		t.Errorf("with cases: outcomes %v", got)
	}
	if got := p["case"].(map[string]any)["enum"]; !slices.Equal(got.([]string), scrum.NothingToDo) {
		t.Errorf("case enum %v", got)
	}
}

// A report that breaks the contract is a tool error saying why, kept in the
// record, and isn't taken as the report.
func TestBadReportsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		c    Contract
		args map[string]any
		want string
	}{
		"unknown outcome":          {Contract{}, map[string]any{"outcome": "great"}, "outcome must be done, failed"},
		"nothing to do, no cases":  {Contract{}, map[string]any{"outcome": OutcomeNothingToDo}, "declares no nothing-to-do cases"},
		"uncited case":             {scrum, map[string]any{"outcome": OutcomeNothingToDo, "evidence": "x"}, "must give one of the routine's cases"},
		"undeclared case":          {scrum, map[string]any{"outcome": OutcomeNothingToDo, "case": "no thread", "evidence": "x"}, "If none applies, report failed"},
		"done without the write":   {scrum, map[string]any{"outcome": OutcomeDone, "evidence": "x"}, "done needs a successful call of post"},
		"nothing_to_do is unknown": {Contract{Done: "x"}, map[string]any{"outcome": OutcomeNothingToDo}, "declares no nothing-to-do cases"},
	} {
		t.Run(name, func(t *testing.T) {
			res := &Result{}
			out, err := outcomeTool(res, tc.c).Call(context.Background(), tc.args)
			if err != nil || !out.IsError || !strings.Contains(resultText(out), tc.want) {
				t.Fatalf("got %q (error %v), want a refusal containing %q", resultText(out), err, tc.want)
			}
			if res.reported != nil {
				t.Error("a refused report was taken")
			}
			if len(res.Calls) != 1 || res.Calls[0].Tool != OutcomeTool || !strings.Contains(res.Calls[0].Error, tc.want) {
				t.Errorf("the refusal isn't in the record: %+v", res.Calls)
			}
		})
	}
	// A fine one is taken, with its case and evidence.
	res := &Result{}
	out, _ := outcomeTool(res, scrum).Call(context.Background(), map[string]any{"outcome": OutcomeNothingToDo, "reason": "r",
		"case": scrum.NothingToDo[0], "evidence": "my reply at 09:12"})
	if out.IsError || verdict(res, scrum) != nil || res.Case != scrum.NothingToDo[0] || res.Evidence != "my reply at 09:12" {
		t.Fatalf("a good report: %q, %+v", resultText(out), res)
	}
}

// After the run, verdict holds the report to the contract too (a report
// that got past the tool, or a structured result counting as done).
func TestVerdictHoldsTheContract(t *testing.T) {
	ended := func(tool string, errText string, intercepted bool) Call {
		return Call{Tool: tool, Write: true, Error: errText, Intercepted: intercepted, ended: true}
	}
	for name, tc := range map[string]struct {
		res  *Result
		c    Contract
		want string // "" = success
	}{
		"uncited case": {&Result{reported: &reported{outcome: OutcomeNothingToDo, reason: "r", kase: "no thread"}}, scrum,
			"without citing one of its cases"},
		"no cases declared": {&Result{reported: &reported{outcome: OutcomeNothingToDo, reason: "r"}}, Contract{},
			"declares no nothing-to-do cases"},
		"cited case": {&Result{reported: &reported{outcome: OutcomeNothingToDo, reason: "r", kase: scrum.NothingToDo[0]}}, scrum, ""},
		"done, writes missing": {&Result{reported: &reported{outcome: OutcomeDone, reason: "r"}}, scrum,
			"reported done, but post never succeeded"},
		"done, the write is still going": {&Result{reported: &reported{outcome: OutcomeDone, reason: "r"},
			Calls: []Call{{Tool: "post", Write: true}}}, scrum, "post never succeeded"},
		"done, a write succeeded": {&Result{reported: &reported{outcome: OutcomeDone, reason: "r"},
			Calls: []Call{ended("post", "", false)}}, scrum, ""},
		"dry run: intercepted": {&Result{DryRun: true, reported: &reported{outcome: OutcomeDone, reason: "r"},
			Calls: []Call{ended("post", "", true)}}, scrum, ""},
		"structured result, writes missing": {&Result{Output: map[string]any{"x": 1}}, scrum, "post never succeeded"},
		"two writes, one missing": {&Result{reported: &reported{outcome: OutcomeDone, reason: "r"},
			Calls: []Call{ended("post", "", false)}}, Contract{Done: "d", Writes: []string{"post", "react"}}, "react never succeeded"},
	} {
		t.Run(name, func(t *testing.T) {
			err := verdict(tc.res, tc.c)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failed *RoutineFailedError
			if !errors.As(err, &failed) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a failure containing %q", err, tc.want)
			}
		})
	}
}

// The instructions are built from the contract: what done means, the exact
// cases, and that anything else, suspicious tool results included, fails.
func TestInstructionsFromTheContract(t *testing.T) {
	in := scrum.Instructions()
	for _, want := range append([]string{scrum.Done, "needs a successful post", "stale", "When unsure, failed"}, scrum.NothingToDo...) {
		if !strings.Contains(in, want) {
			t.Errorf("instructions don't say %q:\n%s", want, in)
		}
	}
	if n := strings.Count(in, "\n"); n > 4 {
		t.Errorf("instructions run to %d lines:\n%s", n+1, in)
	}
	// One case reads as a plain condition; none, no nothing_to_do line.
	one := Contract{Done: "d.", NothingToDo: []string{"it's a holiday"}}.Instructions()
	if !strings.Contains(one, "only if it's a holiday") {
		t.Errorf("one case:\n%s", one)
	}
	if in := (Contract{Done: "d."}).Instructions(); !strings.Contains(in, "nothing_to_do: never") {
		t.Errorf("no cases, yet:\n%s", in)
	}
	if in := (Contract{}).Instructions(); strings.Contains(in, "nothing_to_do") || !strings.Contains(in, OutcomeTool) {
		t.Errorf("without a contract:\n%s", in)
	}
}

// Today's failure: the channel read returns only old messages and the model
// takes it as nothing to do. With the contract, that report is refused and
// the run fails rather than passing as a success.
func TestStaleReadIsNoNothingToDo(t *testing.T) {
	ag := testAgentReading(0, false, script{call("read", nil), reportCase("No Polly message in the channel today")},
		"latest message: April 2 (Polly: Daily Stand-Up)")
	res, err := runWith(t, ag, scrum)
	var failed *RoutineFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, outcome %q: want a failure", err, res.Outcome)
	}
	if c := res.Calls[0]; c.Tool != "read" || !strings.Contains(c.Result, "April 2") {
		t.Errorf("the read's result isn't in the record: %+v", c)
	}
}

// Precedence: a sign-in failure beats a failed write, which beats the report.
func TestVerdictPrecedence(t *testing.T) {
	done := &reported{outcome: OutcomeDone, reason: "ok"}
	failedWrite := []Write{{Tool: "post", Error: "refused"}}
	reauth := &mcp.NeedsReauthError{Server: "chat"}

	if err := verdict(&Result{reauth: reauth, Writes: failedWrite, reported: done}, Contract{}); !errors.Is(err, mcp.ErrNeedsReauth) {
		t.Fatalf("sign-in failure: %v", err)
	}
	if err := verdict(&Result{Writes: failedWrite, reported: done}, Contract{}); err == nil || !strings.Contains(err.Error(), "post failed") {
		t.Fatalf("failed write: %v", err)
	}
	if err := verdict(&Result{reported: done}, Contract{}); err != nil {
		t.Fatalf("clean run: %v", err)
	}
}

// A routine with an output schema that returns a valid result without
// reporting counts as done; reporting still wins.
func TestStructuredResultIsAReport(t *testing.T) {
	res := &Result{Output: map[string]any{"fruit": "fig"}}
	if err := verdict(res, Contract{}); err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("structured result: %v %q", err, res.Outcome)
	}
	res = &Result{Output: map[string]any{"fruit": "fig"}, reported: &reported{outcome: OutcomeFailed, reason: "no fruit"}}
	if err := verdict(res, Contract{}); err == nil {
		t.Fatal("a reported failure was overridden by the structured result")
	}
}

// Every tool call is recorded in order, writes marked, failures with their error.
func TestCallsAreRecorded(t *testing.T) {
	res, _ := run(t, testAgent(1, false, script{call("read", nil), call("post", nil), call("post", nil), report(OutcomeDone)}))
	var got []string
	for _, c := range res.Calls {
		got = append(got, fmt.Sprintf("%s write=%v err=%q", c.Tool, c.Write, c.Error))
	}
	want := []string{`read write=false err=""`, `post write=true err="channel is externally shared"`, `post write=true err=""`}
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("calls:\n%s\nwant first:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(res.Writes) != 2 {
		t.Errorf("writes: %+v", res.Writes)
	}
	// A run without writes says so with an empty list, not null.
	res, _ = run(t, testAgent(0, false, script{call("read", nil), report(OutcomeDone)}))
	if b, _ := json.Marshal(res); !strings.Contains(string(b), `"writes":[]`) {
		t.Errorf("no writes marshals as %s", b)
	}
}

// parallel is a fake model that calls each tool at once, as a model may in
// one turn (claude-code's gateway serves each call in its own goroutine).
type parallel []string

func (parallel) Name() string { return "parallel" }

func (p parallel) Run(ctx context.Context, j Job) (*Output, error) {
	byName := map[string]Tool{}
	for _, t := range j.Tools {
		byName[t.Name] = t
	}
	var wg sync.WaitGroup
	for _, name := range p {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = byName[name].Call(ctx, nil)
		}()
	}
	wg.Wait()
	_, err := byName[OutcomeTool].Call(ctx, map[string]any{"outcome": OutcomeDone, "reason": "ok"})
	return &Output{Text: "finished"}, err
}

// Concurrent tool calls are each recorded, with their own errors (run with -race).
func TestConcurrentCallsAreRecorded(t *testing.T) {
	calls := parallel{}
	for range 10 {
		calls = append(calls, "read", "post")
	}
	ag := testAgent(1000, false, nil) // every post fails
	ag.Backend = calls
	res, _ := run(t, ag)
	// …and the report.
	if res.ToolCalls != len(calls)+1 || len(res.Calls) != len(calls)+1 {
		t.Fatalf("%d tool calls, %d recorded; want %d", res.ToolCalls, len(res.Calls), len(calls)+1)
	}
	for _, c := range res.Calls[:len(calls)] {
		if failed := c.Error != ""; failed != (c.Tool == "post") {
			t.Errorf("call %s has error %q", c.Tool, c.Error)
		}
	}
}
