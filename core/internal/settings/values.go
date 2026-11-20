package settings

import (
	"fmt"
	"time"
)

// Values are every setting's effective value, read from the config file once,
// so whoever needs several settings reads them from one version of the file.
type Values struct {
	byKey map[string]State
}

// Values reads the config file and returns every setting's effective value.
func (s Store) Values() (Values, error) {
	all, err := s.List()
	if err != nil {
		return Values{}, err
	}
	v := Values{byKey: make(map[string]State, len(all))}
	for _, st := range all {
		v.byKey[st.Key] = st
	}
	return v, nil
}

// Get returns one setting's state.
func (v Values) Get(key string) (State, error) {
	st, ok := v.byKey[key]
	if !ok {
		return State{}, unknownKey(key)
	}
	return st, nil
}

// Value is a setting's value (nil for an undeclared key or an unset value
// without a default).
func (v Values) Value(key string) any { return v.byKey[key].Value }

// The typed getters return the zero value for an undeclared key or a value
// of another type.

// Bool is a bool setting's value.
func (v Values) Bool(key string) bool { b, _ := v.Value(key).(bool); return b }

// Int is an int setting's value.
func (v Values) Int(key string) int { n, _ := v.Value(key).(int); return n }

// Float is a number or percent setting's value.
func (v Values) Float(key string) float64 { f, _ := v.Value(key).(float64); return f }

// String is a string, enum, path or cron setting's value.
func (v Values) String(key string) string { s, _ := v.Value(key).(string); return s }

// List is a list setting's value.
func (v Values) List(key string) []string { l, _ := v.Value(key).([]string); return l }

// Duration is a duration setting's value.
func (v Values) Duration(key string) (time.Duration, error) {
	s := v.String(key)
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %w", key, s, err)
	}
	return d, nil
}
