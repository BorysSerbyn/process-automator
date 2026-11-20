package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/secrets"
)

// secretPageTimeout is how long a secret page waits to be filled in.
const secretPageTimeout = 10 * time.Minute

var secretPageHTML = template.Must(template.New("secret").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>procauto sign-in</title>
<style>
:root { color-scheme: light dark; }
body { font: 15px/1.5 -apple-system, system-ui, sans-serif; max-width: 30rem; margin: 4rem auto; padding: 0 1rem; }
input { width: 100%; box-sizing: border-box; padding: .5rem; font: inherit; margin: .5rem 0 1rem; }
button { padding: .5rem 1rem; font: inherit; }
.err { color: #c33; }
.steps { white-space: pre-wrap; font: inherit; }
</style></head><body>
{{if .Done}}<h1>Received</h1><p>procauto has {{.Label}} and is storing it in {{.Store}}; where you started the sign-in says when it's done. You can close this tab.</p>
{{else}}<h1>procauto</h1>
{{if .Steps}}<p><strong>First:</strong></p><pre class="steps">{{.Steps}}</pre>{{end}}
<form method="post"><label>Enter {{.Label}}
<input type="password" name="value" autocomplete="off" autofocus required></label>
{{if .Err}}<p class="err">{{.Err}}</p>{{end}}
<button type="submit">Save</button></form>
<p><small>It goes straight to {{.Store}}.</small></p>{{end}}
</body></html>`))

// secretPage asks for one secret on a local page: a loopback server at an
// unguessable path, so the value goes from the browser to this process only.
// present shows me the page's URL and returns a func to call once it's done.
func secretPage(ctx context.Context, label, steps string, present func(url string) (done func(), err error)) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	path := "/" + hex.EncodeToString(b)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	got := make(chan string, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		data := struct {
			Label, Err, Store, Steps string
			Done                     bool
		}{Label: label, Store: secrets.StoreName, Steps: steps}
		if r.Method == http.MethodPost {
			v := strings.TrimSpace(r.PostFormValue("value"))
			if v == "" {
				data.Err = "Nothing entered."
			} else {
				select {
				case got <- v:
				default: // a repeat submission; the first one counts
				}
				data.Done = true
			}
		}
		_ = secretPageHTML.Execute(w, data)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		// Let the "Received" page finish before closing.
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	done, err := present("http://" + ln.Addr().String() + path)
	if err != nil {
		return "", err
	}
	defer done()
	timer := time.NewTimer(secretPageTimeout)
	defer timer.Stop()
	select {
	case v := <-got:
		return v, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", errors.New("nothing was entered on the page in time")
	}
}
