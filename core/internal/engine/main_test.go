package engine_test

import (
	"errors"
	"os"
	"testing"

	notifyplugin "github.com/BorysSerbyn/process-automator/core/plugins/notify"
)

func TestMain(m *testing.M) {
	// Tests never show desktop notifications on this machine.
	notifyplugin.Deliver = func(notifyplugin.Note) error { return errors.New("tests don't show notifications") }
	os.Exit(m.Run())
}
