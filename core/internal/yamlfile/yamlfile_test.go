package yamlfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Concurrent edits (parallel MCP tool calls, CLI and frontends) all land.
func TestConcurrentEditsAllLand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(path, []byte("# keep me\nother: 1\n"), 0o600)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EditSection(path, "settings", func(s *yaml.Node) error {
				Set(s, fmt.Sprintf("k%d", i), &yaml.Node{Kind: yaml.ScalarNode, Value: "v"})
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	for i := range 20 {
		if !strings.Contains(string(b), fmt.Sprintf("k%d:", i)) {
			t.Fatalf("lost edit k%d:\n%s", i, b)
		}
	}
	if !strings.Contains(string(b), "# keep me") || !strings.Contains(string(b), "other: 1") {
		t.Fatalf("rest of the file not preserved:\n%s", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestDelete(t *testing.T) {
	m := &yaml.Node{Kind: yaml.MappingNode}
	Set(m, "a", &yaml.Node{Kind: yaml.ScalarNode, Value: "1"})
	if !Delete(m, "a") || Delete(m, "a") || Get(m, "a") != nil {
		t.Fatal("delete")
	}
}
