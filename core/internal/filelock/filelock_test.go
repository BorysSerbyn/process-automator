package filelock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Lock(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path, false); !errors.Is(err, ErrHeld) {
		t.Fatalf("second lock: %v, want ErrHeld", err)
	}
	unlock()
	again, err := Lock(path, false)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
}
