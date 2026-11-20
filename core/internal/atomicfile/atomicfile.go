// Package atomicfile writes the app's files whole or not at all, and changes
// shared files under a cross-process lock, so readers never see half a file
// and concurrent writers (the scheduler, commands, frontends, tool calls)
// never lose each other's changes.
package atomicfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/BorysSerbyn/process-automator/core/internal/filelock"
)

// Write replaces path's contents (owner-only, 0600) through a hidden temp file
// in the same folder, creating the folder if needed.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Close(), os.Chmod(tmp, 0o600)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// WriteJSON writes v as indented JSON with Write.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return Write(path, b)
}

// Locked runs fn holding path's cross-process lock (a lock file next to it).
func Locked(path string, fn func() error) error {
	unlock, err := filelock.Lock(path+".lock", true)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// UpdateJSON changes the JSON file at path under its lock: load reads it (a
// pointer, so change can change it), and the result is written with WriteJSON.
// An error from change leaves the file as it was.
func UpdateJSON[T any](path string, load func(path string) (T, error), change func(T) error) error {
	return Locked(path, func() error {
		v, err := load(path)
		if err != nil {
			return err
		}
		if err := change(v); err != nil {
			return err
		}
		return WriteJSON(path, v)
	})
}
