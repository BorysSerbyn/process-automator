package update

// buildSteps build and install a checkout on macOS: `make install` (the app,
// with the core inside, and its login items).
func buildSteps(dir string) ([][]string, error) {
	return [][]string{{"make", "install", "SOURCE_DIR=" + dir}}, nil
}
