// Package secrets stores secrets (credentials, OAuth tokens) behind an interface,
// with a per-OS Default (macOS: Keychain). Callers never pick a backend themselves.
package secrets

import (
	"errors"
	"sync"
)

// encodedPrefix marks values written by the OS stores' Set. Values stored by
// hand (e.g. via `security add-generic-password`) have no prefix and are
// returned as-is.
const encodedPrefix = "b64:"

// ErrNotFound is returned when a secret does not exist.
var ErrNotFound = errors.New("secret not found")

// Lookup reads an optional secret: stored is false when there's none, and err
// is only for a store that can't be read (a locked Keychain or keyring, no
// Secret Service), which must never look like "not stored".
func Lookup(s Store, key string) (value string, stored bool, err error) {
	v, err := s.Get(key)
	switch {
	case errors.Is(err, ErrNotFound):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return v, true, nil
}

// Store reads and writes secrets by key.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Memory is an in-memory Store, for tests.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

// Get implements Store.
func (s *Memory) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set implements Store.
func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

// Delete implements Store.
func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
