// Package osboundary holds only a test: it keeps OS-specific code out of the
// core's shared files (see the architecture rule in CLAUDE.md). OS calls live
// in per-OS files (name_darwin.go, name_unix.go, name_other.go, … or a
// //go:build line); OS-bound integrations are plugins.
package osboundary
