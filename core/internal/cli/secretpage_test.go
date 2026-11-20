package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
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

// Prepare's links: Markdown ones show as their text (in the terminal, with the
// address), bare addresses as themselves, without the sentence's full stop.
func TestPrepareStepLinks(t *testing.T) {
	steps := "1. [Create the app](https://example.com/new?m=%7B%22a%22%3A1%7D) in your workspace.\n" +
		"2. Then open https://example.com/apps."
	if got, want := plainSteps(steps), "1. Create the app (https://example.com/new?m=%7B%22a%22%3A1%7D) in your workspace.\n"+
		"2. Then open https://example.com/apps."; got != want {
		t.Errorf("terminal:\n%s", got)
	}
	want := []stepPart{
		{Text: "1. "},
		{Text: "Create the app", URL: "https://example.com/new?m=%7B%22a%22%3A1%7D"},
		{Text: " in your workspace.\n2. Then open "},
		{Text: "https://example.com/apps", URL: "https://example.com/apps"},
		{Text: "."},
	}
	if got := stepParts(steps); !slices.Equal(got, want) {
		t.Errorf("parts:\n%#v", got)
	}
	if got := stepParts("[run me](javascript:alert(1))"); len(got) != 1 || got[0].URL != "" {
		t.Errorf("only web addresses are links: %#v", got)
	}

	page, err := secretPageWith(t, steps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, `<a href="https://example.com/new?m=%7B%22a%22%3A1%7D" target="_blank" rel="noopener">Create the app</a> in your workspace.`) {
		t.Errorf("page:\n%s", page)
	}
}

// secretPageWith returns the secret page's form as shown with these steps.
func secretPageWith(t *testing.T, steps string) (string, error) {
	t.Helper()
	page := make(chan string, 1)
	_, err := secretPage(context.Background(), "the key", steps, func(u string) (func(), error) {
		go func() {
			res, err := http.Get(u)
			if err != nil {
				t.Error(err)
				page <- ""
				return
			}
			body, _ := io.ReadAll(res.Body)
			page <- string(body)
			_, _ = http.PostForm(u, url.Values{"value": {"x"}})
		}()
		return func() {}, nil
	})
	return <-page, err
}
