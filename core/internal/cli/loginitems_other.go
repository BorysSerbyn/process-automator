//go:build !darwin && !linux

package cli

import (
	"errors"
	"runtime"
)

// loginItemLabels: there are no login items here.
func loginItemLabels() map[string]string { return nil }

func loginItems(map[string]string) ([]LoginItem, error) {
	return nil, errors.New("starting at login isn't implemented on " + runtime.GOOS + " yet")
}

func setLoginItems(map[string]string, bool) error {
	return errors.New("starting at login isn't implemented on " + runtime.GOOS + " yet")
}

const uiName = "menu bar app"

func notHere() error {
	return errors.New("starting and stopping the app isn't implemented on " + runtime.GOOS + " yet")
}

func isSchedulerRunning(string) (bool, error) { return false, notHere() }

func startScheduler(string) error { return notHere() }

func stopScheduler(string) error { return notHere() }

func stopFrontends(string, string, []int) (bool, error) { return false, nil }
