package loginitems

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv(sleeperEnv) == "1" {
		time.Sleep(time.Minute) // a process to stop (Stop's tests)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
