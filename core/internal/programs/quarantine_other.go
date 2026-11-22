//go:build !darwin

package programs

// unquarantine: only macOS marks downloaded files.
func unquarantine(string) error { return nil }
