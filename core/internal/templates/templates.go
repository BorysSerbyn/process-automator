// Package templates reads the templates shipped with the app: routine files
// tracked in the repo so anyone can install them as is or adapt them
// (project-specific ones included), grouped in tag folders, general to
// specific (e.g. projects/acme/standup.prompt). Installing one copies it
// into the routines folder; its parameters' values become per-routine
// settings. The app may ship none: an empty or missing folder lists none.
package templates

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/routines"
)

// Template is one template file.
type Template struct {
	// ID is its tag path and name, e.g. "projects/acme/standup".
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
	// Project is the project it's for (its folder under projects/, e.g.
	// "acme"); empty for every project or none.
	Project     string           `json:"project,omitempty"`
	Description string           `json:"description,omitempty"`
	Platforms   []string         `json:"platforms,omitempty"`
	Params      []routines.Param `json:"params,omitempty"`
	Path        string           `json:"-"`
	Routine     routines.Routine `json:"-"`
}

// ProjectsTag is the folder whose subfolders are projects.
const ProjectsTag = "projects"

// projectOf is the project a template's tags put it in.
func projectOf(tags []string) string {
	if len(tags) >= 2 && tags[0] == ProjectsTag {
		return tags[1]
	}
	return ""
}

// Projects are the projects templates are made for, sorted.
func Projects(list []Template) []string {
	var out []string
	for _, t := range list {
		if t.Project != "" && !slices.Contains(out, t.Project) {
			out = append(out, t.Project)
		}
	}
	slices.Sort(out)
	return out
}

// RunsHere reports whether the template supports this OS.
func (t Template) RunsHere() bool { return t.Routine.RunsHere() }

// List returns every template under dir, by ID; none when dir is empty or
// missing. Each tag folder is read as a routines folder, so templates follow
// exactly the rules routines do; a template that doesn't load is an error
// (templates ship with the app and are tested, so that's a packaging bug).
func List(dir string) ([]Template, error) {
	folders := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, routines.Ext) {
			folders[filepath.Dir(path)] = true
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Template
	for folder := range folders {
		set, err := routines.Load(folder)
		if err != nil {
			return nil, err
		}
		if len(set.Errors) > 0 {
			return nil, fmt.Errorf("template %w", set.Errors[0])
		}
		rel, _ := filepath.Rel(dir, folder)
		var tags []string
		if rel != "." {
			tags = strings.Split(filepath.ToSlash(rel), "/")
		}
		for _, r := range set.Routines {
			out = append(out, Template{
				ID: strings.Join(append(slices.Clone(tags), r.Name), "/"), Name: r.Name, Tags: tags, Project: projectOf(tags),
				Description: r.Description, Platforms: r.Platforms, Params: r.Params, Path: r.Path, Routine: r,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Find returns the template with the given ID.
func Find(dir, id string) (Template, error) {
	all, err := List(dir)
	if err != nil {
		return Template{}, err
	}
	for _, t := range all {
		if t.ID == id {
			return t, nil
		}
	}
	if len(all) == 0 {
		return Template{}, fmt.Errorf("no template %q: this app ships no templates", id)
	}
	ids := make([]string, 0, len(all))
	for _, t := range all {
		ids = append(ids, t.ID)
	}
	return Template{}, fmt.Errorf("no template %q (have: %s)", id, strings.Join(ids, ", "))
}

// Copy writes the template's file into the routines folder as <name>.prompt,
// refusing to overwrite an existing routine.
func Copy(t Template, routinesDir, name string) (string, error) {
	src, err := os.ReadFile(t.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(routinesDir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(routinesDir, name+routines.Ext)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("a routine named %q already exists (%s); install under another name with --as", name, dst)
	}
	if err != nil {
		return "", err
	}
	if _, err := f.Write(src); err != nil {
		_ = f.Close()
		_ = os.Remove(dst)
		return "", err
	}
	return dst, f.Close()
}
