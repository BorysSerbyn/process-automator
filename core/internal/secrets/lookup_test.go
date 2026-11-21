package secrets

import (
	"errors"
	"testing"
)

// failingStore can't be read, like a locked Keychain or keyring.
type failingStore struct{ *Memory }

func (failingStore) Get(string) (string, error) { return "", errors.New("the keychain is locked") }

// Lookup tells a secret that isn't stored from one that can't be read.
func TestLookup(t *testing.T) {
	m := NewMemory()
	if v, stored, err := Lookup(m, "k"); v != "" || stored || err != nil {
		t.Errorf("not stored: %q %v %v", v, stored, err)
	}
	_ = m.Set("k", "v")
	if v, stored, err := Lookup(m, "k"); v != "v" || !stored || err != nil {
		t.Errorf("stored: %q %v %v", v, stored, err)
	}
	if _, stored, err := Lookup(failingStore{NewMemory()}, "k"); stored || err == nil {
		t.Errorf("unreadable: %v %v", stored, err)
	}
}
