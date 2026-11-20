package atomicfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type counter struct{ N int }

func load(path string) (*counter, error) {
	c := &counter{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	return c, json.Unmarshal(b, c)
}

func TestUpdateJSONSerializesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "c.json")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := UpdateJSON(path, load, func(c *counter) error { c.N++; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c, _ := load(path)
	if c.N != 20 {
		t.Fatalf("N = %d, want 20 (a change was lost)", c.N)
	}
	if err := UpdateJSON(path, load, func(c *counter) error { c.N = 0; return errors.New("no") }); err == nil {
		t.Fatal("change's error not returned")
	}
	if c, _ := load(path); c.N != 20 {
		t.Fatal("a failed change was written")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}
