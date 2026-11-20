//go:build !darwin && !linux

package secrets

import (
	"errors"
	"runtime"
)

// Default is the platform's secrets store. None is implemented on this OS yet.
func Default(string) Store { return unsupported{} }

// StoreName is what Default is called, for messages.
const StoreName = "this computer's secret store"

var errUnsupported = errors.New("no secrets store is implemented on " + runtime.GOOS)

type unsupported struct{}

func (unsupported) Get(string) (string, error) { return "", errUnsupported }
func (unsupported) Set(string, string) error   { return errUnsupported }
func (unsupported) Delete(string) error        { return errUnsupported }
