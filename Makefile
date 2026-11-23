.DEFAULT_GOAL := help
SHELL := /bin/bash

# ---- Config (override on the command line or in the environment) -----------
# Another app built on process-automator runs the build targets here from its
# own Makefile (make -C <this checkout> bundle ...), passing its own INSTALL_SH,
# GO_DIR, MAIN, TEMPLATES, LOGO, BUNDLE_PREFIX, SOURCE_DIR, BUILD and VERSION.
#
# The app's installer. Its NAME, COMMAND and FOLDER name the app here as they
# do in the binary (automator.App) and when installing, so they agree.
INSTALL_SH    ?= $(CURDIR)/install.sh
# The Go module and the main package of the app's CLI
GO_DIR        ?= $(CURDIR)/core
MAIN          ?= ./cmd/procauto
# The routine templates the app ships, and its logo (AppIcon.icns, glyph.svg, dot.svg)
TEMPLATES     ?= $(CURDIR)/templates
LOGO          ?= $(CURDIR)/automator-ui/Resources/Logo
# The Linux tray's Go module (any app's: its names and icons come from the package)
TRAY_DIR      ?= $(CURDIR)/automator-tray
# Reverse-DNS prefix of the app's identifiers: the bundle ID is $(BUNDLE_PREFIX).$(CLI),
# the login items $(BUNDLE_PREFIX).$(CLI).ui and .scheduler, the Keychain service $(BUNDLE_PREFIX).$(CLI)
BUNDLE_PREFIX ?= dev
INSTALL_DIR   ?= $(HOME)/Applications
BIN_DIR       ?= $(HOME)/.local/bin
# The checkout development builds read templates from (releases use the bundled ones)
SOURCE_DIR    ?= $(CURDIR)
# Extra codesign flags (releases: --options runtime --timestamp, for notarization)
SIGN_FLAGS    ?=
# CPU architecture of the Linux package (amd64, arm64)
LINUX_ARCH    ?= amd64
# The Linux windows app (Fyne, so cgo): built in Docker (docker), or left out
# of the package (none: the tray opens every window in a terminal)
LINUX_WINDOWS ?= docker
# The GitHub API private releases are read from (empty: api.github.com), and
# the GitHub web address public ones are (empty: github.com)
RELEASE_API   ?=
RELEASE_WEB   ?=
BUILD         ?= $(CURDIR)/build
# Where each step's time is recorded (scripts/timed.sh): make ci sets it, and
# scripts/publish.sh; empty: steps say their time, unrecorded
TIMINGS       ?=
# The Go tests' race detector (-race; empty: without it)
RACE          ?= -race
# The build's version, from git tags (scripts/version.sh)
ifeq ($(origin VERSION),undefined)
VERSION       := $(shell scripts/version.sh)
endif

install_var = $(shell sed -n 's/^$(1)=//p' "$(INSTALL_SH)" | tr -d '"')
APP_NAME  := $(call install_var,NAME)
CLI       := $(call install_var,COMMAND)
FOLDER    := $(call install_var,FOLDER)
SIGN_IDENTITY ?= $(APP_NAME) Dev

BUNDLE_ID := $(BUNDLE_PREFIX).$(CLI)
APP       := $(INSTALL_DIR)/$(APP_NAME).app
UI_LABEL  := $(BUNDLE_ID).ui
SCHED_LABEL := $(BUNDLE_ID).scheduler
# The Linux login items: the tray's XDG autostart entry and the scheduler's systemd user unit
LINUX_UI_LABEL := $(CLI)-tray.desktop
LINUX_SCHED_LABEL := $(CLI)-scheduler.service
CORE_BIN  := $(BUILD)/$(CLI)
APP_BUILD := $(BUILD)/$(APP_NAME).app
DIST      := $(BUILD)/dist
# automator-ui's release build has a build folder of its own: sharing .build
# with swift test's debug build made every release build after the tests
# compile from scratch
UI_SCRATCH := .build-release
# Where swift build puts automator-ui (asked when bundling: it depends on the toolchain)
UI_BIN     = $(shell cd automator-ui && swift build -c release --scratch-path $(UI_SCRATCH) --show-bin-path)/AutomatorUI
# The build's values, set in the automator package (the binary's names are its automator.App)
AUTOMATOR := github.com/BorysSerbyn/process-automator/core/automator
COMMON_LDFLAGS := -s -w -X "$(AUTOMATOR).version=$(VERSION)" -X "$(AUTOMATOR).sourceDir=$(SOURCE_DIR)" \
             -X "$(AUTOMATOR).bundlePrefix=$(BUNDLE_PREFIX)" -X "$(AUTOMATOR).releaseAPI=$(RELEASE_API)" \
             -X "$(AUTOMATOR).releaseWeb=$(RELEASE_WEB)"
LDFLAGS   := $(COMMON_LDFLAGS) -X "$(AUTOMATOR).templatesDir=../Resources/templates"
LINUX_LDFLAGS := $(COMMON_LDFLAGS) -X "$(AUTOMATOR).templatesDir=../share/$(FOLDER)/templates"

# What the installer (scripts/install-app.sh) needs to know about the build
define MACOS_INSTALL_ENV
OS=macos
NAME="$(APP_NAME)"
COMMAND=$(CLI)
FOLDER=$(FOLDER)
VERSION=$(VERSION)
BUNDLE_ID=$(BUNDLE_ID)
UI_LABEL=$(UI_LABEL)
SCHED_LABEL=$(SCHED_LABEL)
KEYCHAIN_SERVICE=$(BUNDLE_ID)
endef
define LINUX_INSTALL_ENV
OS=linux
NAME="$(APP_NAME)"
COMMAND=$(CLI)
FOLDER=$(FOLDER)
VERSION=$(VERSION)
BUNDLE_ID=$(BUNDLE_ID)
UI_LABEL=$(LINUX_UI_LABEL)
SCHED_LABEL=$(LINUX_SCHED_LABEL)
KEYCHAIN_SERVICE=$(BUNDLE_ID)
endef
export MACOS_INSTALL_ENV LINUX_INSTALL_ENV

TIMED = TIMINGS="$(TIMINGS)" "$(CURDIR)/scripts/timed.sh"

.PHONY: contracts help deps build build-core build-ui bundle package package-linux test test-core test-tray test-windows test-ui lint fmt install uninstall release publish ci version clean check-identity

help: ## Show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

deps: ## Install dev tools (Brewfile)
	brew bundle --file=Brewfile

version: ## Print the build version
	@echo $(VERSION)

build: build-core build-ui ## Build the core and automator-ui

# macOS 14 like automator-ui, cgo included (the notify plugin's UserNotifications part)
build-core:
	@mkdir -p "$(BUILD)"
	cd "$(GO_DIR)" && MACOSX_DEPLOYMENT_TARGET=14.0 CGO_CFLAGS="-O2 -g -mmacosx-version-min=14.0" go build -trimpath -ldflags '$(LDFLAGS)' -o "$(CORE_BIN)" $(MAIN)

build-ui:
	@$(TIMED) "· automator-ui (swift build)" 'cd automator-ui && swift build -c release --scratch-path $(UI_SCRATCH)'

bundle: build check-identity ## Assemble and sign the .app
	@printf '%s\n' "$$MACOS_INSTALL_ENV" > "$(BUILD)/install.env"
	SIGN_FLAGS="$(SIGN_FLAGS)" TEMPLATES="$(TEMPLATES)" LOGO="$(LOGO)" \
	  scripts/bundle.sh "$(CORE_BIN)" "$(UI_BIN)" "$(BUILD)" "$(SIGN_IDENTITY)" "$(BUILD)/install.env"

package: bundle ## Release artifact for macOS (build/dist): the signed .app, zipped
	scripts/package.sh macos "$(APP_BUILD)" "$(DIST)"

package-linux: ## Release artifact for Linux (build/dist), for LINUX_ARCH=amd64|arm64: the core, the tray, the windows app (Docker), templates, installer
	@mkdir -p "$(BUILD)/linux-$(LINUX_ARCH)"
	cd "$(GO_DIR)" && CGO_ENABLED=0 GOOS=linux GOARCH=$(LINUX_ARCH) go build -trimpath -ldflags '$(LINUX_LDFLAGS)' \
	  -o "$(BUILD)/linux-$(LINUX_ARCH)/$(CLI)" $(MAIN)
	cd "$(TRAY_DIR)" && CGO_ENABLED=0 GOOS=linux GOARCH=$(LINUX_ARCH) go build -trimpath -ldflags '-s -w' \
	  -o "$(BUILD)/linux-$(LINUX_ARCH)/automator-tray" .
	rm -rf "$(BUILD)/linux-$(LINUX_ARCH)/tray" "$(BUILD)/linux-$(LINUX_ARCH)/automator-windows"
	cd "$(TRAY_DIR)" && go run ./cmd/trayicons "$(LOGO)" "$(BUILD)/linux-$(LINUX_ARCH)/tray"
	@case "$(LINUX_WINDOWS)" in \
	  docker) $(TIMED) "· windows app, linux $(LINUX_ARCH) (docker)" 'scripts/build-linux-windows.sh "$(TRAY_DIR)" $(LINUX_ARCH) "$(BUILD)/linux-$(LINUX_ARCH)/automator-windows"' ;; \
	  none) echo "package-linux: without the windows app (LINUX_WINDOWS=none): the tray opens every window in a terminal" ;; \
	  *) echo "package-linux: LINUX_WINDOWS is docker or none, not $(LINUX_WINDOWS)"; exit 1 ;; \
	esac
	@printf '%s\n' "$$LINUX_INSTALL_ENV" > "$(BUILD)/linux-$(LINUX_ARCH)/install.env"
	TEMPLATES="$(TEMPLATES)" scripts/package.sh linux "$(BUILD)/linux-$(LINUX_ARCH)/$(CLI)" "$(DIST)" $(LINUX_ARCH) \
	  "$(BUILD)/linux-$(LINUX_ARCH)/install.env" "$(BUILD)/linux-$(LINUX_ARCH)/automator-tray" "$(BUILD)/linux-$(LINUX_ARCH)/tray" \
	  $(if $(filter docker,$(LINUX_WINDOWS)),"$(BUILD)/linux-$(LINUX_ARCH)/automator-windows")

contracts: ## Regenerate contracts/ (CLI reference, output schemas, settings schema) from code
	cd core && go run ./cmd/gencontracts ../contracts

test: test-core test-tray test-windows test-ui ## Run all tests (RACE= for the Go ones without the race detector)

test-core:
	cd core && go test $(RACE) ./...

test-tray:
	cd automator-tray && go test $(RACE) ./...

test-windows:
	cd automator-tray/windows && go test $(RACE) ./...

test-ui:
	cd automator-ui && swift test

lint: ## Lint Go and Swift (the tray as built for Linux, too; the windows app as built here, with cgo)
	cd core && golangci-lint run
	cd automator-tray && golangci-lint run && GOOS=linux golangci-lint run
	cd automator-tray/windows && golangci-lint run
	cd automator-ui && swift format lint --recursive --strict Sources Tests

fmt: ## Format Go and Swift
	cd core && gofmt -w .
	cd automator-tray && gofmt -w .
	cd automator-ui && swift format --in-place --recursive Sources Tests

install: bundle ## Build, install and (re)start in place (the installer every install runs)
	INSTALL_DIR="$(INSTALL_DIR)" BIN_DIR="$(BIN_DIR)" "$(APP_BUILD)/Contents/Resources/install-app.sh" install

uninstall: ## Remove the app, CLI link and login items; PURGE=1 also removes config, data, routines folder, logs and secrets
	BIN_DIR="$(BIN_DIR)" "$(APP)/Contents/Resources/install-app.sh" uninstall $(if $(PURGE),--purge)

release: ## Tag a release: make release PART=major|minor|patch
	scripts/release.sh $(PART)

publish: ## Build, sign, test and publish the release tagged at HEAD (SIGN_IDENTITY or SIGN_P12): see scripts/publish.sh
	scripts/publish.sh

ci: export TIMINGS = $(BUILD)/timings.tsv
ci: ## The checks CI ran, here: tests, lint, Linux/Windows builds, shellcheck, the Linux package (Docker, for the windows app), and the macOS artifact's install test; each step timed
	@mkdir -p "$(BUILD)" && : > "$(TIMINGS)"
	@$(TIMED) "test: core" '$(MAKE) test-core'
	@$(TIMED) "test: tray" '$(MAKE) test-tray'
	@$(TIMED) "test: windows app" '$(MAKE) test-windows'
	@$(TIMED) "test: automator-ui (swift)" '$(MAKE) test-ui'
	@$(TIMED) "lint" '$(MAKE) lint'
	@$(TIMED) "cross-compile: core (linux, windows)" 'cd core && GOOS=linux go build ./... && GOOS=windows go build ./...'
	@$(TIMED) "cross-compile: tray (linux amd64, arm64)" 'cd automator-tray && for arch in amd64 arm64; do CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build ./... || exit 1; done'
	@$(TIMED) "shellcheck" 'if command -v shellcheck >/dev/null; then shellcheck install.sh scripts/*.sh; \
	  else echo "shellcheck skipped: not installed (make deps)"; fi'
	@$(TIMED) "test: install-app.sh" 'scripts/test-install-app.sh'
	@$(TIMED) "test: build-linux-windows.sh" 'scripts/test-build-linux-windows.sh'
	@rm -rf "$(BUILD)/ci"
	@$(TIMED) "package: linux amd64" '$(MAKE) package-linux BUNDLE_PREFIX=ci.test DIST="$(BUILD)/ci"'
	@tar -tzf "$(BUILD)"/ci/*-linux-amd64.tar.gz | grep -Eq '^[^/]+/bin/automator-tray$$' && \
	  tar -tzf "$(BUILD)"/ci/*-linux-amd64.tar.gz | grep -Eq '^[^/]+/bin/automator-windows$$' && \
	  tar -tzf "$(BUILD)"/ci/*-linux-amd64.tar.gz | grep -Eq '^[^/]+/share/[^/]+/tray/status-green-dim\.png$$' || \
	  { echo "the Linux package lacks the tray, the windows app or the tray's icons"; exit 1; }
	@$(TIMED) "windows app: linux arm64 (docker; for make publish)" 'scripts/build-linux-windows.sh "$(TRAY_DIR)" arm64 "$(BUILD)/ci/automator-windows-arm64"'
	@$(TIMED) "package: macos" '$(MAKE) package SIGN_IDENTITY=- BUNDLE_PREFIX=ci.test DIST="$(BUILD)/ci"'
	@$(TIMED) "test: install (macos artifact)" 'scripts/test-install.sh "$(BUILD)"/ci/*-macos.zip'
	@scripts/timed.sh --summary "$(TIMINGS)"

clean: ## Remove build output
	rm -rf "$(BUILD)" automator-ui/.build automator-ui/$(UI_SCRATCH)

check-identity:
	@[ "$(SIGN_IDENTITY)" = "-" ] || security find-identity -p codesigning | grep -qF -e "\"$(SIGN_IDENTITY)\"" -e ") $(SIGN_IDENTITY) " || { \
	  echo "Signing identity '$(SIGN_IDENTITY)' not found. Create it (see docs/Guides/Getting started.md) or set SIGN_IDENTITY=..."; exit 1; }
