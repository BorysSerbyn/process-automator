package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The committed contracts must match what the code generates.
func TestContractsUpToDate(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "..", "..", "contracts")
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("contracts/%s is out of date; run `make contracts`", name)
		}
	}
	// Generated files that no longer correspond to a command.
	stale, _ := filepath.Glob(filepath.Join(dir, "cli", "*.schema.json"))
	for _, p := range stale {
		if _, ok := files["cli/"+filepath.Base(p)]; !ok {
			t.Errorf("contracts/cli/%s is stale; run `make contracts`", filepath.Base(p))
		}
	}
}
