package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSecretPage(t *testing.T) {
	ctx := context.Background()
	completed := false
	v, err := secretPage(ctx, "the key", "", func(u string) (func(), error) {
		go func() {
			base, _ := url.Parse(u)
			wrong := *base
			wrong.Path = "/nope"
			if res, err := http.Get(wrong.String()); err != nil || res.StatusCode != http.StatusNotFound {
				t.Errorf("other paths must 404: %v %v", res, err)
			}
			res, err := http.Get(u)
			if err != nil {
				t.Error(err)
				return
			}
			body, _ := io.ReadAll(res.Body)
			if !strings.Contains(string(body), "Enter the key") {
				t.Errorf("form: %s", body)
			}
			res, err = http.PostForm(u, url.Values{"value": {"  s3cret \n"}})
			if err != nil {
				t.Error(err)
				return
			}
			body, _ = io.ReadAll(res.Body)
			if !strings.Contains(string(body), "Received") || strings.Contains(string(body), "s3cret") {
				t.Errorf("after submit: %s", body)
			}
		}()
		return func() { completed = true }, nil
	})
	if err != nil || v != "s3cret" || !completed {
		t.Fatalf("got %q, %v, completed %v", v, err, completed)
	}
}
