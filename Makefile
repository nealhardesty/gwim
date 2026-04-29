# GWiM — Golang Window Manager
#
# This Makefile is the canonical interface for every dev workflow:
# build, test, run, package the .app bundle, manage versions, push releases.
# Per AGENTS.md it is the SINGLE supported way to commit and publish — never
# invoke git add/commit/push directly.
#
# Run `make help` for a categorised list of targets.

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

BINARY        := gwim
PKG           := github.com/nealhardesty/gwim
# Main package lives at the module root so that
#   go install github.com/nealhardesty/gwim@latest
# produces a working binary. `make app` still wraps it in dist/GWiM.app
# with a stable codesign identity for macOS TCC.
CMD           := .
# BUILD_DIR intentionally != "build" to avoid conflicting with the phony
# `make build` target (would produce a "circular dependency dropped" warning).
BUILD_DIR     := dist
APP_NAME      := GWiM
APP_BUNDLE    := $(BUILD_DIR)/$(APP_NAME).app
# Zip uploaded to GitHub Releases (`make release`); ditto keeps .app bundle metadata intact.
RELEASE_ZIP   := $(BUILD_DIR)/$(APP_NAME)-$(VERSION).zip
APP_CONTENTS  := $(APP_BUNDLE)/Contents
APP_MACOS     := $(APP_CONTENTS)/MacOS
APP_RES       := $(APP_CONTENTS)/Resources
ICONSET_DIR   := assets/icon.iconset
ICNS          := $(BUILD_DIR)/icon.icns
PLIST_TPL     := assets/Info.plist.template

VERSION_FILE  := version.go
VERSION       := $(shell awk -F\" '/const Version/ {print $$2}' $(VERSION_FILE))

GO            := go
GOFLAGS       :=
LDFLAGS       := -s -w
BUILD_FLAGS   := -trimpath -ldflags "$(LDFLAGS)"

GREEN := \033[32m
BOLD  := \033[1m
RESET := \033[0m

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help message
	@printf "$(BOLD)GWiM Makefile — version $(VERSION)$(RESET)\n\n"
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.-]+:.*?## / {printf "  $(GREEN)%-22s$(RESET) %s\n", $$1, $$2}' \
		$(MAKEFILE_LIST) | sort

# ---------------------------------------------------------------------------
# Build / run
# ---------------------------------------------------------------------------

# Embedded menu-bar PNGs consumed by //go:embed in internal/icon. These
# are committed to git so that `go install github.com/nealhardesty/gwim@latest`
# works directly from the Go module proxy without needing `make icons`
# first. The auto-regen rule below keeps them in sync with
# scripts/gen-icon/main.go: if a developer changes the drawing code, the
# PNGs get rewritten on the next build and show up as a working-tree
# diff so they get committed alongside the source change.
EMBED_ICONS := internal/icon/assets/icon-active.png internal/icon/assets/icon-suspended.png

$(EMBED_ICONS): scripts/gen-icon/main.go
	@$(GO) run ./scripts/gen-icon

.PHONY: build
build: $(BUILD_DIR)/$(BINARY) ## Compile the gwim binary into ./dist

$(BUILD_DIR)/$(BINARY): $(shell find . \( -name '*.go' -o -name '*.m' \) -not -path './$(BUILD_DIR)/*' -not -path './scripts/*') $(VERSION_FILE) $(EMBED_ICONS)
	@mkdir -p $(BUILD_DIR)
	@echo "==> Building $(BINARY) $(VERSION)"
	$(GO) build $(BUILD_FLAGS) -o $@ $(CMD)

.PHONY: run
run: build ## Build then launch the binary in the foreground (logs to stdout)
	$(BUILD_DIR)/$(BINARY)

.PHONY: run-app
run-app: app ## Build and launch the .app bundle (matches production layout)
	open -W $(APP_BUNDLE)

# ---------------------------------------------------------------------------
# Tests / quality
# ---------------------------------------------------------------------------

.PHONY: test
test: ## Run unit tests with the race detector
	$(GO) test -race ./...

.PHONY: test-short
test-short: ## Run unit tests without -race (CGO race-detection is slow)
	$(GO) test ./...

.PHONY: vet
vet: ## go vet across all packages
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format every Go file with gofmt
	$(GO) fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

.PHONY: lint
lint: vet ## Currently an alias for vet; extend with golangci-lint when adopted

.PHONY: check
check: fmt vet test ## Run fmt, vet, and tests — pre-commit gate

# ---------------------------------------------------------------------------
# Icon / app bundle
# ---------------------------------------------------------------------------

.PHONY: icons
icons: ## Regenerate menu-bar PNGs and the multi-resolution iconset
	$(GO) run ./scripts/gen-icon

# Mark the iconset directory as produced by `icons` so target dependencies
# resolve cleanly when `make app` is invoked from a clean checkout.
$(ICONSET_DIR)/icon_1024x1024.png: scripts/gen-icon/main.go
	@$(GO) run ./scripts/gen-icon

$(ICNS): $(ICONSET_DIR)/icon_1024x1024.png
	@mkdir -p $(BUILD_DIR)
	@echo "==> Converting iconset to icns"
	iconutil --convert icns --output $@ $(ICONSET_DIR)

.PHONY: app
app: build $(ICNS) ## Build the macOS .app bundle into ./dist/GWiM.app (ad-hoc signed)
	@echo "==> Assembling $(APP_BUNDLE)"
	@rm -rf $(APP_BUNDLE)
	@mkdir -p $(APP_MACOS) $(APP_RES)
	@cp $(BUILD_DIR)/$(BINARY) $(APP_MACOS)/$(BINARY)
	@cp $(ICNS) $(APP_RES)/icon.icns
	@sed 's/__VERSION__/$(VERSION)/g' $(PLIST_TPL) > $(APP_CONTENTS)/Info.plist
	@$(MAKE) --no-print-directory codesign
	@touch $(APP_BUNDLE)
	@echo "    Bundle ready: $(APP_BUNDLE)"

.PHONY: codesign
codesign: ## Ad-hoc sign the .app so macOS TCC can persist accessibility permission
	@# CRITICAL: macOS TCC keys Accessibility permission on the binary's
	@# codesign identifier. A bare `go build` produces a binary whose
	@# auto-generated linker signature has Identifier=a.out and an unbound
	@# Info.plist; every rebuild then invalidates the user's permission
	@# silently (System Settings keeps showing it as "granted" but TCC no
	@# longer honours it). Ad-hoc signing the bundle binds Info.plist and
	@# uses CFBundleIdentifier (dev.nealhardesty.gwim) as the stable
	@# codesign identifier, so the permission survives rebuilds — assuming
	@# the bundle ID never changes.
	@if [ ! -d "$(APP_BUNDLE)" ]; then \
	  echo "ERROR: $(APP_BUNDLE) does not exist; run 'make app' first."; exit 1; \
	fi
	@echo "==> Ad-hoc signing $(APP_BUNDLE)"
	@codesign --force --deep --sign - $(APP_BUNDLE)
	@codesign -dv $(APP_BUNDLE) 2>&1 | grep -E '(Identifier|Signature|Sealed|Info.plist)' | sed 's/^/    /'

.PHONY: install
install: app ## Copy GWiM.app to /Applications, killing any running instance
	@echo "==> Stopping any running GWiM instance"
	-@pkill -f '/Applications/$(APP_NAME).app/Contents/MacOS/$(BINARY)' 2>/dev/null || true
	@sleep 1
	@echo "==> Installing to /Applications"
	rm -rf /Applications/$(APP_NAME).app
	cp -R $(APP_BUNDLE) /Applications/
	@echo ""
	@echo "Installed. If hotkeys don't fire after launch:"
	@echo "  1. Open System Settings -> Privacy & Security -> Accessibility"
	@echo "  2. Remove any old GWiM entry (-)"
	@echo "  3. Re-add /Applications/GWiM.app and toggle ON"
	@echo "  4. Quit & relaunch GWiM"

open: install ## Open the installed app
	open -a /Applications/$(APP_NAME).app


.PHONY: clean
clean: ## Remove build artifacts (binary, .app, multi-resolution iconset)
	rm -rf $(BUILD_DIR)
	rm -rf $(ICONSET_DIR)
	@# NOTE: internal/icon/assets/*.png are committed (required for
	@# `go install` to work). Use `make icons` to regenerate them.

# ---------------------------------------------------------------------------
# Versioning
# ---------------------------------------------------------------------------

.PHONY: version
version: ## Print the current version
	@echo $(VERSION)

.PHONY: version-increment
version-increment: ## Bump PATCH (use BUMP=major|minor|patch to choose)
	@$(MAKE) --no-print-directory _bump BUMP=$${BUMP:-patch}

.PHONY: _bump
_bump:
	@awk -v bump=$(BUMP) -F'"' ' \
	  /const Version/ { \
	    n = split($$2, p, "."); \
	    if (bump=="major") { p[1]+=1; p[2]=0; p[3]=0 } \
	    else if (bump=="minor") { p[2]+=1; p[3]=0 } \
	    else { p[3]+=1 } \
	    new = p[1]"."p[2]"."p[3]; \
	    sub(/"[^"]+"/, "\""new"\""); \
	    print "==> Bumping " $$2 " -> " new > "/dev/stderr"; \
	  } \
	  { print } \
	' $(VERSION_FILE) > $(VERSION_FILE).tmp && mv $(VERSION_FILE).tmp $(VERSION_FILE)
	@gofmt -w $(VERSION_FILE)

# ---------------------------------------------------------------------------
# Release / publish
# ---------------------------------------------------------------------------
#
# `make push` is the ONLY sanctioned commit / publish path (per AGENTS.md).

.PHONY: push
push: check ## Bump patch, build, commit, push, tag — full release cycle
	@$(MAKE) --no-print-directory version-increment
	@$(MAKE) --no-print-directory app
	@NEW_VERSION=$$(awk -F\" '/const Version/ {print $$2}' $(VERSION_FILE)); \
	 echo "==> Committing v$$NEW_VERSION"; \
	 git add -A; \
	 git commit -m "release: v$$NEW_VERSION"; \
	 git push; \
	 git tag "v$$NEW_VERSION"; \
	 git push origin "v$$NEW_VERSION"; \
	 echo "==> Released v$$NEW_VERSION"

# Build the signed .app, zip it, and attach to GitHub Releases for tag v$(VERSION).
# Requires: GitHub CLI (`brew install gh`), `gh auth login`, and git tag v$(VERSION)
# on this repo (e.g. after `make push`). If the release already exists, re-uploads
# the zip (--clobber) so you can refresh the asset for the same version.
.PHONY: release
release: app ## Zip GWiM.app and create or update GitHub release for current version (needs gh)
	@command -v gh >/dev/null 2>&1 || { \
	  echo "ERROR: gh (GitHub CLI) not found. Install: https://cli.github.com/"; exit 1; }
	@gh auth status >/dev/null 2>&1 || { \
	  echo "ERROR: gh is not logged in. Run: gh auth login"; exit 1; }
	@if ! git rev-parse -q --verify "refs/tags/v$(VERSION)" >/dev/null 2>&1; then \
	  echo "ERROR: git tag v$(VERSION) not found locally."; \
	  echo "       Create it first (e.g. \`make push\`) or \`git fetch --tags\`."; \
	  exit 1; \
	fi
	@echo "==> Zipping $(APP_BUNDLE) -> $(RELEASE_ZIP)"
	@rm -f $(RELEASE_ZIP)
	@ditto -c -k --sequesterRsrc --keepParent $(APP_BUNDLE) $(RELEASE_ZIP)
	@echo "==> GitHub release v$(VERSION)"
	@if gh release view "v$(VERSION)" >/dev/null 2>&1; then \
	  gh release upload "v$(VERSION)" "$(RELEASE_ZIP)" --clobber; \
	  echo "    Uploaded $(RELEASE_ZIP) to existing release."; \
	else \
	  gh release create "v$(VERSION)" "$(RELEASE_ZIP)" \
	    --title "$(APP_NAME) v$(VERSION)" --generate-notes; \
	  echo "    Created release with $(RELEASE_ZIP)."; \
	fi

# ---------------------------------------------------------------------------
# Convenience
# ---------------------------------------------------------------------------

.PHONY: deps
deps: ## Download / verify go module dependencies
	$(GO) mod download
	$(GO) mod verify
