package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/BorysSerbyn/process-automator/core/internal/yamlfile"
)

// sectionKey is the top-level key in the config file that holds settings.
const sectionKey = "settings"

// State is a setting with its effective value.
type State struct {
	Setting
	Value     any  `json:"value"`
	IsDefault bool `json:"isDefault"`
	// ValueText and DefaultText are the values written the way `settings set`
	// takes them ("" when unset), for frontends to show and send back as is.
	ValueText   string `json:"valueText"`
	DefaultText string `json:"defaultText"`
	// Inactive says why the setting has no effect now: it's for another
	// llm.backend than the one in use.
	Inactive string `json:"inactive,omitempty"`
}

// Store reads and writes settings in the "settings:" section of the config file,
// editing the YAML in place so the rest of the file (and its comments) is preserved.
type Store struct {
	Path string
	// Extra are settings declared at runtime (per-routine settings from routine files).
	Extra []Setting
}

func (s Store) all() []Setting { return append(append([]Setting{}, Registry...), s.Extra...) }

func (s Store) lookup(key string) (Setting, bool) { return lookupIn(s.all(), key) }

// List returns every setting with its effective value, in registry order.
func (s Store) List() ([]State, error) {
	values, err := s.read()
	if err != nil {
		return nil, err
	}
	defs := s.all()
	out := make([]State, 0, len(defs))
	for _, def := range defs {
		st := State{Setting: def, Value: def.Default, IsDefault: true, DefaultText: def.Text(def.Default)}
		if raw, ok := values[def.Key]; ok {
			v, err := def.Normalize(raw)
			if err != nil {
				return nil, fmt.Errorf("config %s: %w", s.Path, err)
			}
			st.Value, st.IsDefault = v, false
		}
		st.ValueText = def.Text(st.Value)
		out = append(out, st)
	}
	markInactive(out)
	return out, nil
}

// markInactive marks settings for another llm.backend than the one in use.
func markInactive(states []State) {
	backend := ""
	for _, st := range states {
		if st.Key == "llm.backend" {
			backend, _ = st.Value.(string)
		}
	}
	for i, st := range states {
		if st.Backend != "" && st.Backend != backend {
			states[i].Inactive = "only used with llm.backend " + st.Backend
		}
	}
}

// Get returns one setting's state.
func (s Store) Get(key string) (State, error) {
	if _, ok := s.lookup(key); !ok {
		return State{}, unknownKey(key)
	}
	v, err := s.Values()
	if err != nil {
		return State{}, err
	}
	return v.Get(key)
}

// Set validates input and writes it.
func (s Store) Set(key, input string) (State, error) {
	def, ok := s.lookup(key)
	if !ok {
		return State{}, unknownKey(key)
	}
	v, err := def.Parse(input)
	if err != nil {
		return State{}, err
	}
	if err := s.edit(func(section *yaml.Node) error {
		var val yaml.Node
		if err := val.Encode(v); err != nil {
			return err
		}
		yamlfile.Set(section, key, &val)
		return nil
	}); err != nil {
		return State{}, err
	}
	return State{Setting: def, Value: v, ValueText: def.Text(v), DefaultText: def.Text(def.Default)}, nil
}

// Reset removes an override so the default applies.
func (s Store) Reset(key string) (State, error) {
	def, ok := s.lookup(key)
	if !ok {
		return State{}, unknownKey(key)
	}
	err := s.edit(func(section *yaml.Node) error {
		yamlfile.Delete(section, key)
		return nil
	})
	return State{Setting: def, Value: def.Default, IsDefault: true, ValueText: def.Text(def.Default), DefaultText: def.Text(def.Default)}, err
}

func unknownKey(key string) error {
	return fmt.Errorf("unknown setting %q (see `procauto settings list`)", key)
}

// read returns the raw values in the settings section.
func (s Store) read() (map[string]any, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Settings map[string]any `yaml:"settings"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("config %s: %w", s.Path, err)
	}
	if file.Settings == nil {
		file.Settings = map[string]any{}
	}
	return file.Settings, nil
}

// edit applies fn to the settings section of the config file, in place.
func (s Store) edit(fn func(section *yaml.Node) error) error {
	return yamlfile.EditSection(s.Path, sectionKey, fn)
}

// ResetPrefix removes every override whose key starts with prefix (e.g. a
// deleted routine's "routines.<name>."), declared or not.
func (s Store) ResetPrefix(prefix string) error {
	return s.edit(func(section *yaml.Node) error {
		for i := 0; i+1 < len(section.Content); {
			if strings.HasPrefix(section.Content[i].Value, prefix) {
				section.Content = append(section.Content[:i], section.Content[i+2:]...)
				continue
			}
			i += 2
		}
		return nil
	})
}
