package claudemcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path, err := ConfigPath()
	if err != nil || path != filepath.Join(dir, ".claude.json") {
		t.Fatalf("config path %q %v", path, err)
	}
	if _, ok, err := UserServer(path, "app"); ok || err != nil {
		t.Errorf("no config file: %v %v", ok, err)
	}
	_ = os.WriteFile(path, []byte(`{"projects":{"/x":{"mcpServers":{"app":{"command":"/p"}}}},`+
		`"mcpServers":{"app":{"type":"stdio","command":"/bin/app","args":["mcp","serve"],"env":{}}}}`), 0o600)
	s, ok, err := UserServer(path, "app")
	if err != nil || !ok || s.Command != "/bin/app" || len(s.Args) != 2 {
		t.Errorf("registered: %+v %v %v", s, ok, err)
	}
	if _, ok, _ := UserServer(path, "other"); ok {
		t.Error("found a server that isn't there")
	}
	_ = os.WriteFile(path, []byte(`{`), 0o600)
	if _, _, err := UserServer(path, "app"); err == nil {
		t.Error("a broken config file read as fine")
	}
}
