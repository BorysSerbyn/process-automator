package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// SecretService stores secrets in the Secret Service keyring, one item per
// key, all with one service attribute, through libsecret's `secret-tool`
// (package libsecret-tools; no cgo). Values are base64-encoded like the
// Keychain's, so any content survives; values stored by hand are returned as-is.
type SecretService struct {
	Service string
}

// errNoSecretTool: without secret-tool nothing can be stored, so nothing is
// found or deleted either; storing says what to install.
var errNoSecretTool = errors.New("secret-tool isn't installed (install libsecret-tools, or your distribution's libsecret package)")

// secretTool runs secret-tool (tests replace it).
var secretTool = func(stdin string, args ...string) (stdout []byte, err error) {
	cmd := exec.Command("secret-tool", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errNoSecretTool
		}
		// A message means it failed (no keyring, D-Bus); a bare non-zero
		// exit means nothing matched (lookup, clear).
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%v: %s", err, msg)
		}
	}
	return out, err
}

func (s SecretService) attrs(key string) []string {
	return []string{"service", s.Service, "account", key}
}

// Get implements Store. secret-tool lookup fails, printing nothing, when there's no such item.
func (s SecretService) Get(key string) (string, error) {
	out, err := secretTool("", append([]string{"lookup"}, s.attrs(key)...)...)
	if err != nil {
		var exit *exec.ExitError
		if errors.Is(err, errNoSecretTool) || errors.As(err, &exit) && len(out) == 0 {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("secret service get %q: %w", key, err)
	}
	v := strings.TrimRight(string(out), "\n")
	if enc, ok := strings.CutPrefix(v, encodedPrefix); ok {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return "", fmt.Errorf("secret service get %q: corrupt value: %w", key, err)
		}
		return string(b), nil
	}
	return v, nil
}

// Set implements Store. The value goes on stdin, never argv.
func (s SecretService) Set(key, value string) error {
	enc := encodedPrefix + base64.StdEncoding.EncodeToString([]byte(value))
	args := append([]string{"store", "--label", "procauto: " + key}, s.attrs(key)...)
	if _, err := secretTool(enc, args...); err != nil {
		return fmt.Errorf("secret service set %q: %w", key, err)
	}
	return nil
}

// Delete implements Store. Deleting a missing key is not an error.
func (s SecretService) Delete(key string) error {
	if _, err := secretTool("", append([]string{"clear"}, s.attrs(key)...)...); err != nil {
		var exit *exec.ExitError
		if errors.Is(err, errNoSecretTool) || errors.As(err, &exit) {
			return nil // clear fails when nothing matched
		}
		return fmt.Errorf("secret service delete %q: %w", key, err)
	}
	return nil
}
