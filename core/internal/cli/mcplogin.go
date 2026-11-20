package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BorysSerbyn/process-automator/core/internal/engine"
	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// Sign-in through an MCP client (`auth login` as a tool). Pages, the OAuth
// sign-in and the local secret page, are shown to me with URL elicitation,
// so secrets never pass through the client or the model.
//
// A sign-in can need several pages, and on current protocol versions a tool
// can't ask the client anything mid-call: it returns the page as an input
// request, and the client calls again once it's shown (for older clients the
// SDK does those round trips itself). So the sign-in runs in the background
// across calls, found again through the call's request state. A client that
// can't show URLs gets the page opened in the browser here, since this server
// runs on my computer.

// loginTimeout bounds a sign-in left waiting (a page never shown or filled in).
const loginTimeout = secretPageTimeout + 5*time.Minute

// loginStep is what a background sign-in needs next: a page to show, or
// (url empty) that it finished.
type loginStep struct {
	url, message, id string
	err              error
}

// pendingLogin is a sign-in in progress. It is the loginUI the sign-in uses.
type pendingLogin struct {
	id      string
	session *sdk.ServerSession
	urlOK   bool               // the client can show URLs
	steps   chan loginStep     // from the sign-in
	shown   chan bool          // to it: whether I went on to the page
	cancel  context.CancelFunc // stops the sign-in
	mu      sync.Mutex
	said    []string // everything said, for the final result
	prepare string   // what to do first (Plugin.Prepare), also on the secret page
	unsent  []string // said since the last page, shown with the next one
	notify  func(string)
	opened  []string // elicitation IDs shown, completed at the end
}

// loginRegistry holds the sign-ins waiting for their next call.
type loginRegistry struct {
	mu sync.Mutex
	m  map[string]*pendingLogin
}

func (r *loginRegistry) put(p *pendingLogin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]*pendingLogin{}
	}
	r.m[p.id] = p
}

func (r *loginRegistry) get(id string) *pendingLogin {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[id]
}

func (r *loginRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// clientShowsURLs reports whether the client declared URL elicitation.
func clientShowsURLs(ss *sdk.ServerSession) bool {
	ip := ss.InitializeParams()
	if ip == nil || ip.Capabilities == nil || ip.Capabilities.Elicitation == nil {
		return false
	}
	return ip.Capabilities.Elicitation.URL != nil
}

// mcpLogin is the auth_login tool: it starts a sign-in, or continues one after
// the client showed a page, and returns the next page or the outcome.
func mcpLogin(ctx context.Context, s *automatorServer, req *sdk.CallToolRequest, t cliTool, args map[string]any) (*sdk.CallToolResult, error) {
	var p *pendingLogin
	if state := req.Params.RequestState; state != "" {
		if p = s.logins.get(state); p == nil {
			return nil, errors.New("that sign-in is no longer waiting (it finished or timed out); call auth_login again")
		}
		went := false
		for _, r := range req.Params.InputResponses {
			if er, ok := r.(*sdk.ElicitResult); ok && er.Action == "accept" {
				went = true
			}
		}
		p.setNotify(progressNotifier(ctx, req))
		select {
		case p.shown <- went:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		name, _ := args[t.args[0].prop].(string)
		if name == "" {
			return nil, fmt.Errorf("%s is required", t.args[0].prop)
		}
		a, err := engine.Load(s.configPath, withAutomator(s.configPath, s.plugins), secretStore())
		if err != nil {
			return nil, err
		}
		p = s.startLogin(a, name, req.Session)
		p.setNotify(progressNotifier(ctx, req))
	}

	select {
	case st := <-p.steps:
		if st.url != "" {
			return &sdk.CallToolResult{
				InputRequests: sdk.InputRequestMap{st.id: &sdk.ElicitParams{
					Mode: "url", Message: st.message, URL: st.url, ElicitationID: st.id,
				}},
				RequestState: p.id,
			}, nil
		}
		s.logins.remove(p.id)
		p.complete()
		text := strings.Join(p.allSaid(), "\n")
		if st.err != nil {
			return toolText(strings.TrimSpace(text+"\n"+st.err.Error()), true), nil
		}
		return toolText(text, false), nil
	case <-ctx.Done():
		// The call was cancelled: so is the sign-in.
		p.cancel()
		s.logins.remove(p.id)
		return nil, ctx.Err()
	}
}

// startLogin runs a sign-in in the background.
func (s *automatorServer) startLogin(a *engine.App, name string, ss *sdk.ServerSession) *pendingLogin {
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	p := &pendingLogin{
		id: randomID(), session: ss, urlOK: clientShowsURLs(ss),
		steps: make(chan loginStep, 1), shown: make(chan bool), cancel: cancel,
	}
	s.logins.put(p)
	go func() {
		defer cancel()
		defer s.logins.remove(p.id)
		err := login(ctx, a, name, p)
		p.steps <- loginStep{err: err} // buffered: delivered even if nobody calls again
	}()
	return p
}

// progressNotifier sends a line as a progress notification on this call, if
// the client asked for progress.
func progressNotifier(ctx context.Context, req *sdk.CallToolRequest) func(string) {
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	n := 0
	return func(msg string) {
		n++
		_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: token, Progress: float64(n), Message: msg})
	}
}

func (p *pendingLogin) setNotify(f func(string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notify = f
}

// Say implements loginUI: shown now as progress (instructions such as
// approving a pairing matter while they happen), with the next page, and in
// the result.
func (p *pendingLogin) Say(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.mu.Lock()
	p.said = append(p.said, msg)
	p.unsent = append(p.unsent, msg)
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		notify(msg)
	}
}

func (p *pendingLogin) allSaid() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.said...)
}

// Prepare implements loginUI: the steps are shown as progress and on the
// secret page, which is where I confirm by going on.
func (p *pendingLogin) Prepare(_ context.Context, server, steps string) error {
	p.mu.Lock()
	p.prepare = steps
	p.mu.Unlock()
	p.Say("Before signing in to %s:\n%s", server, steps)
	return nil
}

// Secret implements loginUI with the local secret page.
func (p *pendingLogin) Secret(ctx context.Context, _, label string) (string, error) {
	p.mu.Lock()
	steps := p.prepare
	p.mu.Unlock()
	return secretPage(ctx, label, steps, func(url string) (func(), error) {
		return func() {}, p.present(ctx, url, "Enter "+label+" on this page; it goes straight to "+secrets.StoreName+".")
	})
}

// SignIn implements loginUI with the server's OAuth page.
func (p *pendingLogin) SignIn(ctx context.Context, server, url string) error {
	return p.present(ctx, url, "Sign in to "+server+" for procauto.")
}

// present shows me a page: through the client, which then calls again, or
// opened here when the client can't show URLs.
func (p *pendingLogin) present(ctx context.Context, url, message string) error {
	p.mu.Lock()
	if len(p.unsent) > 0 {
		message = strings.Join(p.unsent, "\n") + "\n\n" + message
		p.unsent = nil
	}
	p.mu.Unlock()
	if !p.urlOK {
		if err := openExternal(url); err != nil {
			return fmt.Errorf("this client can't show the page and no browser opened (%v); run `procauto auth login` in a terminal", err)
		}
		p.Say("Opened a page in your browser: %s", message)
		return nil
	}
	id := randomID()
	select {
	case p.steps <- loginStep{url: url, message: message, id: id}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case went := <-p.shown:
		if !went {
			return errors.New("you didn't go on to the page")
		}
		p.mu.Lock()
		p.opened = append(p.opened, id)
		p.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// complete tells the client the pages it showed are done with.
func (p *pendingLogin) complete() {
	p.mu.Lock()
	ids := p.opened
	p.opened = nil
	p.mu.Unlock()
	for _, id := range ids {
		_ = p.session.NotifyElicitationComplete(context.Background(), &sdk.ElicitationCompleteParams{ElicitationID: id})
	}
}
