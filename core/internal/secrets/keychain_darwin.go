package secrets

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// Keychain stores secrets as generic passwords in the login Keychain, one item per key,
// all under one service name. It shells out to /usr/bin/security (no cgo).
type Keychain struct {
	Service string
}

// Get implements Store.
func (k Keychain) Get(key string) (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", k.Service, "-a", key, "-w").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("keychain get %q: %w", key, err)
	}
	v := strings.TrimRight(string(out), "\n")
	if enc, ok := strings.CutPrefix(v, encodedPrefix); ok {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return "", fmt.Errorf("keychain get %q: corrupt value: %w", key, err)
		}
		return string(b), nil
	}
	return v, nil
}

// Set implements Store. The value is passed on stdin (interactive mode), not argv,
// so it never shows up in the process list; it's base64-encoded so any content is safe to quote.
func (k Keychain) Set(key, value string) error {
	enc := encodedPrefix + base64.StdEncoding.EncodeToString([]byte(value))
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l %s -w %s\n",
		quote(k.Service), quote(key), quote(product.Command+": "+key), quote(enc)))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() > 0 {
		return fmt.Errorf("keychain set %q: %v %s", key, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Delete implements Store. Deleting a missing key is not an error.
func (k Keychain) Delete(key string) error {
	err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", k.Service, "-a", key).Run()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("keychain delete %q: %w", key, err)
	}
	return nil
}

// quote wraps s for security(1)'s interactive parser. Inputs here contain no quotes
// (service/key names we control, base64 values), which the check enforces.
func quote(s string) string {
	if strings.ContainsAny(s, "\"\\\n") {
		panic("secrets: unquotable value")
	}
	return `"` + s + `"`
}
