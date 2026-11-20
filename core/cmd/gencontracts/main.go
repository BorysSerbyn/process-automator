// Command gencontracts writes the generated CLI contract into a directory
// (normally ../contracts). See internal/contract.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BorysSerbyn/process-automator/core/internal/contract"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gencontracts <contracts-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	files, err := contract.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	old, _ := filepath.Glob(filepath.Join(dir, "cli", "*.schema.json"))
	for _, p := range old {
		_ = os.Remove(p)
	}
	for name, b := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d contract files to %s\n", len(files), dir)
}
