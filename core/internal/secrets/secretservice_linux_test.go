package secrets

import (
	"os/exec"
	"slices"
	"testing"
)

// fakeSecretTool keeps items like secret-tool does, keyed by the attributes.
func fakeSecretTool(t *testing.T) map[string]string {
	t.Helper()
	items := map[string]string{}
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
			if !ok {
				return nil, notFound
			}
			return []byte(v), nil
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
	return items
}

func TestSecretServiceRoundTrip(t *testing.T) {
	items := fakeSecretTool(t)
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
