package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Notes are a release's notes, as its GitHub release has them.
type Notes struct {
	Tag string `json:"tag"`
	// Body is the notes, in Markdown (GitHub's).
	Body string `json:"body"`
	// URL is the release's page.
	URL         string     `json:"url,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	// FetchedAt is when they were read from GitHub.
	FetchedAt time.Time `json:"fetchedAt"`
}

// NotesFresh is how long notes read from GitHub are used before they're
// read again (a release's notes can be edited after it's published).
const NotesFresh = 24 * time.Hour

// maxNotes caps a release's answer.
const maxNotes = 1 << 20

// Notes reads the release's notes from the GitHub API: a private
// repository's with its token, a public one's with none (the web pages
// don't serve them in a form worth reading). At most one release is asked
// for each time, well within the API's limit for no token.
func (s Source) Notes(ctx context.Context, tag string) (Notes, error) {
	if s.Repo == "" {
		return Notes{}, errors.New("this build doesn't know where its releases are")
	}
	if !semver.IsValid(tag) {
		return Notes{}, fmt.Errorf("%q isn't a release (vX.Y.Z)", tag)
	}
	api := strings.TrimSuffix(s.API, "/") + "/repos/" + s.Repo + "/releases/tags/" + url.PathEscape(tag)
	// Public releases' Source sends no token, private ones' their token.
	resp, err := s.get(ctx, api, "application/vnd.github+json")
	if notFound(err) {
		return Notes{}, fmt.Errorf("GitHub has no release %s", tag)
	}
	if err != nil {
		return Notes{}, fmt.Errorf("release %s's notes: %w", tag, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var rel struct {
		Tag         string     `json:"tag_name"`
		Body        string     `json:"body"`
		URL         string     `json:"html_url"`
		PublishedAt *time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxNotes)).Decode(&rel); err != nil {
		return Notes{}, fmt.Errorf("release %s's notes: %w", tag, err)
	}
	return Notes{Tag: tag, Body: strings.TrimSpace(rel.Body), URL: rel.URL, PublishedAt: rel.PublishedAt}, nil
}

// CachedNotes are the release's notes from dir, read again from src when
// they're older than NotesFresh (or there are none); a copy kept from
// before stands in when they can't be read.
func CachedNotes(ctx context.Context, src Source, dir, tag string, now time.Time) (Notes, error) {
	path := filepath.Join(dir, filepath.Base(tag)+".json")
	var kept Notes
	b, err := os.ReadFile(path)
	haveKept := err == nil && json.Unmarshal(b, &kept) == nil && kept.Tag == tag
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Notes{}, err
	}
	if haveKept && now.Sub(kept.FetchedAt) < NotesFresh && !now.Before(kept.FetchedAt) {
		return kept, nil
	}
	n, err := src.Notes(ctx, tag)
	if err != nil {
		if haveKept {
			return kept, nil
		}
		return Notes{}, err
	}
	n.FetchedAt = now
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = atomicfile.WriteJSON(path, n)
	}
	return n, nil
}
