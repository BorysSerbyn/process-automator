package cli

import "testing"

// launchctl print-disabled says "=> disabled/enabled" (current macOS) or
// "=> true/false" (older).
func TestDisabledLine(t *testing.T) {
	out := `disabled services = {
	"dev.x.procauto.ui" => disabled
	"dev.x.procauto.scheduler" => enabled
	"com.old.style" => true
	"com.old.other" => false
}`
	got := map[string]string{}
	for _, m := range disabledLine.FindAllStringSubmatch(out, -1) {
		got[m[1]] = m[2]
	}
	if got["dev.x.procauto.ui"] != "disabled" || got["dev.x.procauto.scheduler"] != "enabled" || got["com.old.style"] != "true" || len(got) != 4 {
		t.Fatalf("parsed %v", got)
	}
}
