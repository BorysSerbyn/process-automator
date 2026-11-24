// Package statusline puts the app in Claude Code's status line, so the
// Claude plan's usage (which Claude Code gives a status line command) is
// recorded when the app can't read it from /usage. It wraps whatever status
// line I have: Claude Code runs the app's command, which records the usage
// and runs mine with the same input, so my status line looks the same.
// Only the status line entry of Claude Code's settings is changed; the rest
// of the file keeps its keys and their order. Where that entry is and its
// shape are the Claude Code version's (Format, from its protocol).
package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/atomicfile"
)

// Marker ends the app's status line command ("<procauto> usage statusline").
const Marker = "usage statusline"

// Format is how a Claude Code version keeps its status line: its settings
// file, the entry's key in it, and the entry's type and command fields.
type Format struct {
	SettingsPath string
	Key          string
	Type         string // the entry's type for a command
	TypeField    string
	CommandField string
}

// command is the entry's command.
func (f Format) command(entry json.RawMessage) string {
	var v map[string]any
	_ = json.Unmarshal(entry, &v)
	c, _ := v[f.CommandField].(string)
	return c
}

// saved is my status line as it was before the app wrapped it.
type saved struct {
	// StatusLine is the status line entry, absent when there was none.
	StatusLine json.RawMessage `json:"statusLine,omitempty"`
}

// Installed reports whether Claude Code's status line runs the app's command.
func Installed(f Format) (bool, error) {
	sl, err := statusLineOf(f)
	if err != nil || sl == nil {
		return false, err
	}
	return strings.HasSuffix(strings.TrimSpace(f.command(sl)), Marker), nil
}

// Install makes Claude Code's status line run command (the app's), saving the
// one I had in savedPath to run and to restore. Already installed, it only
// updates the command (e.g. the app moved), keeping what was saved.
func Install(f Format, savedPath, command string) error {
	settingsPath := f.SettingsPath
	return atomicfile.Locked(settingsPath, func() error {
		b, err := readSettings(settingsPath)
		if err != nil {
			return err
		}
		obj, err := parseObject(b)
		if err != nil {
			return fmt.Errorf("%s: %w", settingsPath, err)
		}
		current := obj.get(f.Key)
		entry := map[string]any{f.TypeField: f.Type}
		if current != nil {
			_ = json.Unmarshal(current, &entry)
		}
		if !strings.HasSuffix(strings.TrimSpace(f.command(current)), Marker) {
			if err := atomicfile.WriteJSON(savedPath, saved{StatusLine: current}); err != nil {
				return err
			}
		}
		entry[f.TypeField], entry[f.CommandField] = f.Type, command
		raw, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		obj.set(f.Key, raw)
		return atomicfile.Write(settingsPath, obj.encode())
	})
}

// Uninstall puts back the status line I had (none, if I had none).
func Uninstall(f Format, savedPath string) error {
	settingsPath := f.SettingsPath
	if ok, err := Installed(f); err != nil || !ok {
		return err
	}
	s, err := loadSaved(savedPath)
	if err != nil {
		return err
	}
	err = atomicfile.Locked(settingsPath, func() error {
		b, err := readSettings(settingsPath)
		if err != nil {
			return err
		}
		obj, err := parseObject(b)
		if err != nil {
			return fmt.Errorf("%s: %w", settingsPath, err)
		}
		obj.set(f.Key, s.StatusLine) // nil removes it
		return atomicfile.Write(settingsPath, obj.encode())
	})
	if err != nil {
		return err
	}
	return os.Remove(savedPath)
}

// Original is the status line command the app wraps ("" when I had none).
func Original(f Format, savedPath string) (string, error) {
	s, err := loadSaved(savedPath)
	if err != nil || s.StatusLine == nil {
		return "", err
	}
	return f.command(s.StatusLine), nil
}

func loadSaved(path string) (saved, error) {
	var s saved
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func statusLineOf(f Format) (json.RawMessage, error) {
	b, err := readSettings(f.SettingsPath)
	if err != nil {
		return nil, err
	}
	obj, err := parseObject(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.SettingsPath, err)
	}
	return obj.get(f.Key), nil
}

// readSettings reads the settings file; none is an empty object.
func readSettings(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("{}"), nil
	}
	return b, err
}

// object is a JSON object's members in their order.
type object struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseObject(b []byte) (*object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	obj := &object{values: map[string]json.RawMessage{}}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := obj.values[key]; !dup {
			obj.keys = append(obj.keys, key)
		}
		obj.values[key] = v
	}
	return obj, nil
}

func (o *object) get(key string) json.RawMessage { return o.values[key] }

// set replaces a member (added at the end when new); nil removes it.
func (o *object) set(key string, v json.RawMessage) {
	if v == nil {
		delete(o.values, key)
		for i, k := range o.keys {
			if k == key {
				o.keys = append(o.keys[:i], o.keys[i+1:]...)
				break
			}
		}
		return
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// encode writes the object indented by two spaces, as Claude Code does.
func (o *object) encode() []byte {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteString(",")
		}
		key, _ := json.Marshal(k)
		var v bytes.Buffer
		if err := json.Indent(&v, o.values[k], "  ", "  "); err != nil {
			v.Write(o.values[k])
		}
		fmt.Fprintf(&buf, "\n  %s: %s", key, v.Bytes())
	}
	if len(o.keys) > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}
