package secrets

import (
	"errors"
	"os"
	"testing"
)

// Touches the real login Keychain; opt in with PROCAUTO_KEYCHAIN_TEST=1.
func TestKeychainRoundTrip(t *testing.T) {
	if os.Getenv("PROCAUTO_KEYCHAIN_TEST") == "" {
		t.Skip("set PROCAUTO_KEYCHAIN_TEST=1 to run")
	}
	k := Keychain{Service: "dev.procauto.test"}
	key := "roundtrip"
	val := `{"json":"with \"quotes\" and\nnewlines"}`
	t.Cleanup(func() { _ = k.Delete(key) })

	if err := k.Set(key, val); err != nil {
		t.Fatal(err)
	}
	got, err := k.Get(key)
	if err != nil || got != val {
		t.Fatalf("Get = %q, %v; want %q", got, err, val)
	}
	if err := k.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}
