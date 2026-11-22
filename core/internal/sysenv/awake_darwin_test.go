package sysenv

import "testing"

func TestFullyAwakeReadsTheGraphicsCapability(t *testing.T) {
	for _, c := range []struct {
		ioreg string
		want  bool
	}{
		{`  "System Capabilities" = 15`, true}, // awake
		{`  "System Capabilities" = 9`, false}, // maintenance wake: CPU and network only
		{`  "System Capabilities" = 13`, false},
		{`no such property`, true}, // can't tell: awake
	} {
		if got := fullyAwake([]byte(c.ioreg)); got != c.want {
			t.Errorf("%q: got %v, want %v", c.ioreg, got, c.want)
		}
	}
}

func TestFullyAwakeNow(t *testing.T) {
	if !FullyAwake() {
		t.Fatal("the computer running the tests is awake")
	}
}
