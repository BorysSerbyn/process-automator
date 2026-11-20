.DEFAULT_GOAL := help
SHELL := /bin/bash

# ---- Config (override on the command line or in the environment) -----------
# Reverse-DNS prefix of the app's identifiers: the bundle ID is $(BUNDLE_PREFIX).procauto,
# the login items $(BUNDLE_PREFIX).procauto.ui and .scheduler
BUNDLE_PREFIX ?= dev
SIGN_IDENTITY ?= Process Automator Dev
INSTALL_DIR   ?= $(HOME)/Applications
BIN_DIR       ?= $(HOME)/.local/bin
# The checkout development builds read templates from (releases use the bundled ones)
SOURCE_DIR    ?= $(CURDIR)
# Keychain service for the app's secrets (built into the binary; cleared by PURGE=1)
KEYCHAIN_SERVICE ?= $(BUNDLE_PREFIX).procauto
# Extra codesign flags (releases: --options runtime --timestamp, for notarization)
SIGN_FLAGS    ?=
# UNIVERSAL=1 builds the macOS app for Apple silicon and Intel (releases)
UNIVERSAL     ?=
# CPU architecture of the Linux package (amd64, arm64)
LINUX_ARCH    ?= amd64
# Where releases come from and who signs them: read from install.sh, so the
# installer, `update` and the release build agree
RELEASE_REPO  := $(shell sed -n 's/^REPO=//p' install.sh)
TEAM_ID       := $(shell sed -n 's/^TEAM_ID=//p' install.sh)
RELEASE_API   ?= https://api.github.com

VERSION   := $(shell scripts/version.sh)
BUILD     := build
APP       := $(INSTALL_DIR)/Process Automator.app
UI_LABEL  := $(BUNDLE_PREFIX).procauto.ui
SCHED_LABEL := $(BUNDLE_PREFIX).procauto.scheduler
# The Linux scheduler's systemd user unit (Linux has no automator-ui)
LINUX_SCHED_LABEL := procauto-scheduler.service
CORE_BIN  := $(BUILD)/procauto
APP_BUILD := $(BUILD)/Process Automator.app
DIST      := $(BUILD)/dist
ifdef UNIVERSAL
SWIFT_ARCHS := --arch arm64 --arch x86_64
endif
# Where swift build puts automator-ui (asked when bundling: it depends on the toolchain and the archs)
UI_BIN     = $(shell cd automator-ui && swift build -c release $(SWIFT_ARCHS) --show-bin-path)/AutomatorUI
PKG       := github.com/BorysSerbyn/process-automator/core/internal
COMMON_LDFLAGS := -s -w -X $(PKG)/version.Version=$(VERSION) \
             -X "$(PKG)/version.SourceDir=$(SOURCE_DIR)" \
             -X "$(PKG)/cli.keychainService=$(KEYCHAIN_SERVICE)" \
             -X "$(PKG)/version.ReleaseRepo=$(RELEASE_REPO)" -X "$(PKG)/version.ReleaseAPI=$(RELEASE_API)" \
             -X "$(PKG)/version.TeamID=$(TEAM_ID)"
LDFLAGS   := $(COMMON_LDFLAGS) -X "$(PKG)/cli.uiLabel=$(UI_LABEL)" -X "$(PKG)/cli.schedulerLabel=$(SCHED_LABEL)" \
             -X "$(PKG)/version.TemplatesDir=../Resources/templates"
LINUX_LDFLAGS := $(COMMON_LDFLAGS) -X "$(PKG)/cli.uiLabel=" -X "$(PKG)/cli.schedulerLabel=$(LINUX_SCHED_LABEL)" \
             -X "$(PKG)/version.TemplatesDir=../share/process-automator/templates"

# What the installer (scripts/install-app.sh) needs to know about the build
define MACOS_INSTALL_ENV
OS=macos
VERSION=$(VERSION)
BUNDLE_ID=$(BUNDLE_PREFIX).procauto
UI_LABEL=$(UI_LABEL)
SCHED_LABEL=$(SCHED_LABEL)
KEYCHAIN_SERVICE=$(KEYCHAIN_SERVICE)
endef
define LINUX_INSTALL_ENV
OS=linux
VERSION=$(VERSION)
BUNDLE_ID=$(BUNDLE_PREFIX).procauto
UI_LABEL=
SCHED_LABEL=$(LINUX_SCHED_LABEL)
KEYCHAIN_SERVICE=$(KEYCHAIN_SERVICE)
endef
export MACOS_INSTALL_ENV LINUX_INSTALL_ENV

.PHONY: contracts help deps build build-core build-ui bundle package package-linux test lint fmt install uninstall release version clean check-identity

help: ## Show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

deps: ## Install dev tools (Brewfile)
	brew bundle --file=Brewfile

version: ## Print the build version
	@echo $(VERSION)

build: build-core build-ui ## Build the core and automator-ui

build-core:
	@mkdir -p $(BUILD)
ifdef UNIVERSAL
	cd core && GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o ../$(CORE_BIN)-arm64 ./cmd/procauto
	cd core && GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o ../$(CORE_BIN)-amd64 ./cmd/procauto
	lipo -create -output $(CORE_BIN) $(CORE_BIN)-arm64 $(CORE_BIN)-amd64
else
	cd core && go build -trimpath -ldflags '$(LDFLAGS)' -o ../$(CORE_BIN) ./cmd/procauto
endif

build-ui:
	cd automator-ui && swift build -c release $(SWIFT_ARCHS)

bundle: build check-identity ## Assemble and sign the .app
	@printf '%s\n' "$$MACOS_INSTALL_ENV" > $(BUILD)/install.env
	SIGN_FLAGS="$(SIGN_FLAGS)" scripts/bundle.sh $(CORE_BIN) $(UI_BIN) $(BUILD) $(VERSION) $(BUNDLE_PREFIX) "$(SIGN_IDENTITY)" $(BUILD)/install.env

package: bundle ## Release artifact for macOS (build/dist): the signed .app, zipped
	scripts/package.sh macos "$(APP_BUILD)" $(DIST)

package-linux: ## Release artifact for Linux (build/dist), for LINUX_ARCH=amd64|arm64: the core, templates, installer
	@mkdir -p $(BUILD)/linux-$(LINUX_ARCH)
	cd core && CGO_ENABLED=0 GOOS=linux GOARCH=$(LINUX_ARCH) go build -trimpath -ldflags '$(LINUX_LDFLAGS)' \
	  -o ../$(BUILD)/linux-$(LINUX_ARCH)/procauto ./cmd/procauto
	@printf '%s\n' "$$LINUX_INSTALL_ENV" > $(BUILD)/linux-$(LINUX_ARCH)/install.env
	scripts/package.sh linux $(BUILD)/linux-$(LINUX_ARCH)/procauto $(DIST) $(LINUX_ARCH) $(BUILD)/linux-$(LINUX_ARCH)/install.env

contracts: ## Regenerate contracts/ (CLI reference, output schemas, settings schema) from code
	cd core && go run ./cmd/gencontracts ../contracts

test: ## Run all tests
	cd core && go test -race ./...
	cd automator-ui && swift test

lint: ## Lint Go and Swift
	cd core && golangci-lint run
	cd automator-ui && swift format lint --recursive --strict Sources Tests

fmt: ## Format Go and Swift
	cd core && gofmt -w .
	cd automator-ui && swift format --in-place --recursive Sources Tests

install: bundle ## Build, install and (re)start in place (the installer every install runs)
	INSTALL_DIR="$(INSTALL_DIR)" BIN_DIR="$(BIN_DIR)" "$(APP_BUILD)/Contents/Resources/install-app.sh" install

uninstall: ## Remove the app, CLI link and login items; PURGE=1 also removes config, data, routines folder, logs and secrets
	BIN_DIR="$(BIN_DIR)" "$(APP)/Contents/Resources/install-app.sh" uninstall $(if $(PURGE),--purge)

release: ## Tag a release: make release PART=major|minor|patch
	scripts/release.sh $(PART)

clean: ## Remove build output
	rm -rf $(BUILD) automator-ui/.build

check-identity:
	@[ "$(SIGN_IDENTITY)" = "-" ] || security find-identity -v -p codesigning | grep -q "\"$(SIGN_IDENTITY)\"" || { \
	  echo "Signing identity '$(SIGN_IDENTITY)' not found. Create it (see README) or set SIGN_IDENTITY=..."; exit 1; }
