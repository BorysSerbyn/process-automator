//go:build !darwin && !linux

package loginitems

import (
	"errors"
	"runtime"
)

// Labels: there are no login items here.
func Labels() map[string]string { return nil }

// List says starting at login isn't implemented here.
func List(map[string]string) ([]LoginItem, error) {
	return nil, errors.New("starting at login isn't implemented on " + runtime.GOOS + " yet")
}

// Set says starting at login isn't implemented here.
func Set(map[string]string, bool) error {
	return errors.New("starting at login isn't implemented on " + runtime.GOOS + " yet")
}

// UIName is what the frontend is called here.
const UIName = "menu bar app"

func notHere() error {
	return errors.New("starting and stopping the app isn't implemented on " + runtime.GOOS + " yet")
}

func isSchedulerRunning(string) (bool, error) { return false, notHere() }

func startScheduler(string) error { return notHere() }

func stopScheduler(string) error { return notHere() }

func stopFrontends(string, string, []int) (bool, error) { return false, nil }
