package engine

import (
	"testing"

	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// The scheduler checks in a fresh child of itself (macOS caches answers in a
// process that keeps running): the child's answer is what's recorded.
func TestPermissionsCheckedInAFreshChild(t *testing.T) {
	found := checkInChild()
	if len(found) != len(permissions.Names()) || found["camera"] != permissions.Missing || found["notifications"] != permissions.Granted {
		t.Fatalf("the child's answer: %v", found)
	}
}
