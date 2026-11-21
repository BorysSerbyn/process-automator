// Package app is the app this tray serves, as the package it was installed
// from names it: one automator-tray serves every app built on the engine, as
// automator-ui reads its Info.plist (both come from the build's install.env).
package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Info is the app: its names, its version, and where its CLI and the
// tray's icons are.
type Info struct {
	// Name is what people see: the menu, the notifications ("Process Automator").
	Name string
	// Command is the CLI's name, and CLI the binary itself, beside the tray.
	Command string
	CLI     string
	// Version is the build's version.
	Version string
	// BundleID names the app's identifiers (the tray's D-Bus name derives from it).
	BundleID string
	// Icons is the folder of the tray's icons (made from the app's logo
	// when it was packaged).
	Icons string
}

// IconsFolder is the tray's icons folder, beside install.env.
const IconsFolder = "tray"

// Discover finds the app of the tray at exe, from the Linux tree it was
// packaged in: exe is <root>/bin/automator-tray, the CLI <root>/bin/<COMMAND>,
// and <root>/share/<FOLDER>/ holds install.env and the tray's icons. dir,
// when set, is that share folder (development and tests).
func Discover(exe, dir string) (Info, error) {
	if dir == "" {
		root := filepath.Dir(filepath.Dir(exe))
		matches, _ := filepath.Glob(filepath.Join(root, "share", "*", IconsFolder))
		var found []string
		for _, m := range matches {
			if _, err := os.Stat(filepath.Join(filepath.Dir(m), "install.env")); err == nil {
				found = append(found, filepath.Dir(m))
			}
		}
		switch len(found) {
		case 0:
			return Info{}, fmt.Errorf("no app beside %s: expected %s", exe,
				filepath.Join(root, "share", "<folder>", "install.env"))
		case 1:
			dir = found[0]
		default:
			return Info{}, fmt.Errorf("more than one app beside %s: %s", exe, strings.Join(found, ", "))
		}
	}
	env, err := readEnv(filepath.Join(dir, "install.env"))
	if err != nil {
		return Info{}, err
	}
	info := Info{
		Name: env["NAME"], Command: env["COMMAND"], Version: env["VERSION"], BundleID: env["BUNDLE_ID"],
		Icons: filepath.Join(dir, IconsFolder),
	}
	if info.Name == "" || info.Command == "" || strings.ContainsRune(info.Command, '/') {
		return Info{}, fmt.Errorf("%s doesn't name the app (NAME, COMMAND)", filepath.Join(dir, "install.env"))
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	info.CLI = filepath.Join(filepath.Dir(exe), info.Command)
	if st, err := os.Stat(info.CLI); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return Info{}, fmt.Errorf("no %s CLI beside the tray (%s)", info.Name, info.CLI)
	}
	return info, nil
}

// readEnv reads install.env: KEY=value lines, values optionally in double
// quotes (it's sourced by the installer's shell).
func readEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		env[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(env) == 0 {
		return nil, errors.New(path + " is empty")
	}
	return env, nil
}
