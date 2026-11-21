package secrets

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// fakeSecretTool keeps items like secret-tool does, keyed by the attributes.
// Keys in locked are in a locked keyring: lookup finds nothing, as
// secret-tool's does when no prompt can unlock it; search still lists them.
func fakeSecretTool(t *testing.T) (items map[string]string, locked map[string]bool) {
	t.Helper()
	items, locked = map[string]string{}, map[string]bool{}
	orig := secretTool
	t.Cleanup(func() { secretTool = orig })
	secretTool = func(stdin string, args ...string) ([]byte, error) {
		notFound := exec.Command("false").Run() // a bare non-zero exit
		switch args[0] {
		case "store":
			i := slices.Index(args, "service")
			items[args[i+1]+"/"+args[i+3]] = stdin
			return nil, nil
		case "lookup":
			v, ok := items[args[2]+"/"+args[4]]
			if !ok || locked[args[4]] {
				return nil, notFound
			}
			return []byte(v), nil
		case "search":
			if _, ok := items[args[2]+"/"+args[4]]; ok {
				return []byte("[/1]\nlabel = x\nattribute.account = " + args[4] + "\n"), nil
			}
			return nil, nil
		case "clear":
			k := args[2] + "/" + args[4]
			if _, ok := items[k]; !ok {
				return nil, notFound
			}
			delete(items, k)
			return nil, nil
		}
		t.Fatalf("unexpected secret-tool %v", args)
		return nil, nil
	}
	return items, locked
}

func TestSecretServiceRoundTrip(t *testing.T) {
	items, _ := fakeSecretTool(t)
	s := SecretService{Service: "test.procauto"}
	if _, err := s.Get("k"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	value := "multi\nline \"quoted\" value"
	if err := s.Set("k", value); err != nil {
		t.Fatal(err)
	}
	if items["test.procauto/k"] == value {
		t.Fatal("stored unencoded")
	}
	if got, err := s.Get("k"); err != nil || got != value {
		t.Fatalf("Get = %q, %v", got, err)
	}
	items["test.procauto/by-hand"] = "plain\n"
	if got, _ := s.Get("by-hand"); got != "plain" {
		t.Fatalf("hand-stored value = %q", got)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
}

// A secret in a locked keyring isn't "not found" (which would read as never
// signed in): it says the keyring is locked.
func TestSecretServiceLockedKeyring(t *testing.T) {
	_, locked := fakeSecretTool(t)
	s := SecretService{Service: "test.procauto"}
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	locked["k"] = true
	_, err := s.Get("k")
	if err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("Get from a locked keyring: %v", err)
	}
	if _, err := s.Get("missing"); err != ErrNotFound {
		t.Fatalf("a missing key while locked: %v", err)
	}
}
