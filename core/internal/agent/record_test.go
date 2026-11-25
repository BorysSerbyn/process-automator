package agent

import (
	"strings"
	"testing"
)

// Each call keeps its result, when it was made, and whether a dry run
// intercepted it; a failed one its error.
func TestRecordKeepsResults(t *testing.T) {
	res, err := run(t, testAgent(1, false, script{call("read", nil), call("post", nil), call("post", nil), report(OutcomeDone)}))
	if err != nil {
		t.Fatal(err)
	}
	c := res.Calls
	if c[0].Result != "hello" || c[0].At == nil || c[1].Error != "channel is externally shared" || c[1].Result != "" ||
		c[2].Result != "posted" || c[3].Tool != OutcomeTool || c[3].Result != "Recorded." {
		t.Fatalf("calls: %+v", c)
	}
	res, _ = run(t, testAgent(0, true, script{call("read", nil), call("post", nil), report(OutcomeDone)}))
	if c := res.Calls; c[0].Intercepted || !c[1].Intercepted || !strings.Contains(c[1].Result, "DRY RUN") {
		t.Fatalf("dry run: %+v", c)
	}
}

// A result past the per-call cap is clipped and says so; once the run's cap
// is spent, results are clipped to what's left of it.
func TestRecordTruncates(t *testing.T) {
	res := &Result{}
	big := strings.Repeat("é", CallResultCap) // 2 bytes each: twice the cap
	i := res.startCall("read", "chat", nil, false)
	res.endCall(i, callEnd{result: big})
	c := res.Calls[i]
	if !c.Truncated || c.ResultSize != len(big) || len(c.Result) > CallResultCap || !strings.HasSuffix(c.Result, "é") {
		t.Fatalf("per call: truncated %v, size %d, kept %d", c.Truncated, c.ResultSize, len(c.Result))
	}
	for range RunResultCap / CallResultCap {
		i = res.startCall("read", "chat", nil, false)
		res.endCall(i, callEnd{result: strings.Repeat("x", CallResultCap)})
	}
	if res.kept > RunResultCap {
		t.Fatalf("kept %d bytes, cap %d", res.kept, RunResultCap)
	}
	last := res.Calls[len(res.Calls)-1]
	if !last.Truncated || last.Result != "" || last.ResultSize != CallResultCap {
		t.Fatalf("past the run's cap: %+v", last)
	}
	// Short results still fit nothing once it's spent, and say so.
	i = res.startCall("read", "chat", nil, false)
	res.endCall(i, callEnd{result: "ok"})
	if c := res.Calls[i]; !c.Truncated || c.Result != "" {
		t.Fatalf("after the cap: %+v", c)
	}
	// An error is kept up to the per-call cap, whatever the run's.
	i = res.startCall("post", "chat", nil, true)
	res.endCall(i, callEnd{err: "refused: " + strings.Repeat("x", 2*CallResultCap)})
	if c := res.Calls[i]; !c.Truncated || len(c.Error) != CallResultCap || res.Writes[len(res.Writes)-1].Error != c.Error {
		t.Fatalf("error: truncated %v, %d bytes", c.Truncated, len(c.Error))
	}
}

// What looks like a credential isn't kept: secret-named arguments, token
// shapes, and key=value pairs, in arguments, results and errors.
func TestRecordRedacts(t *testing.T) {
	res := &Result{}
	args := map[string]any{"text": "use Bearer abcdefghijklmnopqrstuvwxyz", "api_key": "k-123",
		"nested": map[string]any{"password": "hunter22", "list": []any{"xoxb-1234567890-abcdef"}}, "page_token": "next-42"}
	i := res.startCall("post", "chat", args, true)
	res.endCall(i, callEnd{result: `{"access_token": "ya29.abcdefgh", "ok": true} and sk-ant-0123456789abcdefghij`})
	c := res.Calls[i]
	kept := c.Args
	nested := kept["nested"].(map[string]any)
	if kept["api_key"] != Redacted || nested["password"] != Redacted || nested["list"].([]any)[0] != Redacted ||
		strings.Contains(kept["text"].(string), "abcdefghijklmnop") || kept["page_token"] != "next-42" {
		t.Fatalf("args: %+v", kept)
	}
	if strings.Contains(c.Result, "ya29") || strings.Contains(c.Result, "sk-ant") || !strings.Contains(c.Result, `"ok": true`) {
		t.Fatalf("result: %s", c.Result)
	}
	// The tool itself got the arguments as they were.
	if args["api_key"] != "k-123" || args["nested"].(map[string]any)["password"] != "hunter22" {
		t.Fatalf("the call's own arguments were changed: %+v", args)
	}
}
