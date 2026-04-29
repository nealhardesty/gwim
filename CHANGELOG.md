# Changelog

All notable changes to **GWiM** are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- **Alt-Tab overlay on all displays.** The switcher opens one mirrored
  borderless panel per connected `NSScreen`, each centred in that display's
  visible (working) area and scaled independently up to **90%** of that
  monitor's width and height (uniform scale, preserving layout when many
  windows wrap to multiple rows). Mirrored displays still show a single panel.

- **Removed automatic remote-desktop suspension.** GWiM no longer
  auto-suspends when remote-desktop, VNC, or Screen Sharing apps are
  foreground. Use the menu bar or **`Ctrl+Alt+X`** to suspend or resume
  hotkeys. The `Engine` no longer takes a blocklist; the permission
  poller only refreshes Accessibility and Screen Recording state for the
  tray.

### Added

- **Alt-Tab window switcher** (per `ALTTAB.md`). New chord `⌥⇥` / `⌥⇧⇥`
  opens mirrored borderless overlays (one per physical display) centred in
  each screen's working area, showing every standard window across all
  running regular apps, MRU-ordered with the currently focused window pinned
  to position 0. Holding
  Option keeps the overlay open; repeated `⇥` / `⇧⇥` advance the
  highlight; releasing Option commits and raises the selected window
  via AX `kAXRaiseAction` + `NSRunningApplication activateWithOptions:`.
  The same actions appear in a new tray **Window Switcher** submenu
  category — clicking from the menu opens the overlay in modal mode
  where `↩` commits and `⎋` cancels. New packages:
  `internal/altswitch` (platform-agnostic MRU stash with race-clean
  tests) and `internal/platform/macos/altswitch_native.m` (one `NSWindow` + custom
  `NSView` per display, `CGEventTap` for the in-overlay key
  handling, AX-driven window enumeration + raise).

- **Live window thumbnails in the switcher.** Each slot now shows a
  ScreenCaptureKit snapshot of the underlying window (macOS 14+) with
  the app icon as a corner badge. `SCShareableContent` is fetched once
  per overlay open and cached for the duration of the capture batch;
  per-window snapshots use `SCScreenshotManager.captureImageWithFilter:`
  with a 1 s timeout. Bridged to sync via dispatch semaphores because
  thumbnail capture happens off the main thread. Failure for any slot
  (denied permission, occluded window, owning process gone) silently
  falls back to a centred app icon for that slot only.

- **Screen Recording permission row in the tray.** Mirrors the
  Accessibility row: probes `CGPreflightScreenCaptureAccess` on every
  poller tick, displays "Screen Recording: granted ✓" or "off — click
  to enable thumbnails", and on click triggers
  `CGRequestScreenCaptureAccess` + opens System Settings → Privacy &
  Security → Screen Recording. The engine grew a parallel
  `Config.ScreenRecordingCheck` and `RefreshScreenRecording()` to keep
  this row honest. Switcher functionality does NOT depend on the
  permission — denial just removes thumbnails.

- **Open at Login** menu-bar checkbox (macOS 13+, from `GWiM.app` only)
  using `SMAppService` to register or unregister the main app as a login
  item. Failures surface in the tray’s “Last action” row.

- **`make release`** — builds the `.app` bundle, zips it with `ditto` to
  `dist/GWiM-<version>.zip`, then uses `gh` to create the GitHub release
  `v<version>` (with `--generate-notes`) or, if the release already exists,
  uploads the zip with `--clobber`. Requires a local `git tag v<version>`
  (for example after `make push`) and an authenticated `gh` CLI.

### Changed

- **`make push` no longer requires a pending `CHANGELOG.md` edit.** The
  Makefile used to abort if `CHANGELOG.md` was unchanged since the last
  commit; that gate is removed.

- **`go install github.com/nealhardesty/gwim@latest` now works.** Two
  obstacles were removed:
  1. **Main package moved to the module root.** `cmd/gwim/main.go`,
     `main_darwin.go`, `main_windows.go`, and `version.go` were
     relocated to `./` and the now-empty `cmd/gwim/` directory was
     deleted. The `Makefile` was updated (`CMD := .`,
     `VERSION_FILE := version.go`) so all existing targets keep
     working unchanged.
  2. **Embedded icon PNGs are now committed to git.** The two ~200-byte
     menu-bar PNGs in `internal/icon/assets/` (consumed by `//go:embed`
     in `internal/icon/icon.go`) were previously gitignored and only
     produced by `make icons`, which broke any `go install` from the
     module proxy with `pattern assets/icon-active.png: no matching
     files found`. They are now tracked. The Makefile's auto-regen
     rule (`$(EMBED_ICONS): scripts/gen-icon/main.go`) is preserved
     so changes to the drawing code regenerate the PNGs on the next
     build, surfacing them as a working-tree diff to be committed
     alongside the source change.
- `make clean` no longer deletes the embedded PNGs (they are now
  tracked source assets).
- README.md gained an "Option 1: `go install`" section explaining the
  new install path and its trade-off vs. the recommended
  `make app` + `make install` flow (which still pins the codesign
  identity for stable macOS Accessibility permission).

### Fixed

- **Hotkeys silently stopped working after rebuilds.** Root cause was
  macOS TCC keying Accessibility permission on the binary's codesign
  identifier; a bare `go build` produced binaries with `Identifier=a.out`
  and an unbound Info.plist, so every rebuild silently invalidated the
  user's permission grant — System Settings still showed it as granted,
  but TCC no longer honoured it for the new binary. Hotkeys fired, AX
  calls returned "denied", and the failure was logged to launchd where
  no one would see it. Fixed in three layers:
  1. `make codesign` ad-hoc signs the bundle with
     `CFBundleIdentifier`-derived stable identity and a sealed
     Info.plist; baked into `make app` so every build is signed.
  2. `make install` now stops any running instance and prints clear
     re-grant instructions for the one-time TCC reset.
  3. The tray now shows a live "Accessibility: granted ✓ / DENIED" row
     (clickable — opens System Settings → Privacy & Security →
     Accessibility directly), plus a hidden "Last action: …" row that
     appears when the engine catches an action error. The engine
     re-checks AX after every failed action so the row flips the
     instant TCC revokes the grant.

### Added

- **`Engine.Config.AccessibilityCheck`** — optional non-prompting
  callback the engine polls every tick to keep the AX state honest.
  Exposed via `SuspensionState.AccessibilityGranted` and
  `SuspensionState.AccessibilityChecked`.
- **`Engine.RefreshAccessibility()`** — manual re-check, called by the
  tray's AX-row click handler so toggling permission in System Settings
  is reflected immediately rather than after the next poll tick.
- **`SuspensionState.LastActionError`** — most recent failed action
  message, surfaced by the tray when non-empty.
- `make codesign` Makefile target plus `make install` lifecycle improvements.
- **`Ctrl+Alt+X` global toggle** for GWiM enable/disable. The hotkey is
  registered as **persistent** so it stays bound even while regular
  shortcuts are suspended (e.g. during a screen-sharing session). The
  same behaviour is bound to the menu-bar Suspend/Activate item.
- **User-mode override semantics**: the toggle now beats automatic
  suspension. New `UserMode` enum (`Auto` / `ForceActive` /
  `ForceSuspended`) on the engine; toggling while auto-suspended forces
  GWiM on, satisfying the use case of "I'm in screen sharing but I want
  to rearrange local windows".
- New `wm.HotkeyManager.RegisterPersistent` interface method, with
  matching macOS implementation that simply skips persistent bindings
  during `SetSuspended`.
- Tray status menu now distinguishes `Active`, `Active (forced on)`,
  `Auto-suspended (blocklist)`, and `Suspended (forced off)`.
- Tray Suspend/Activate label now displays its accelerator (`⌃⌥X`).

### Changed

- `Engine.SetUserSuspended(bool)` now sets `UserModeForceSuspended` /
  `UserModeForceActive` (legacy boolean → tri-state mapping). Existing
  `Snapshot().UserSuspended` semantics preserved as `UserMode == ForceSuspended`.
- Tray click on Suspend/Activate now goes through `Engine.ToggleUserSuspended()`
  for parity with the hotkey path.

### Tests

- New `TestToggle_FlipsActiveAndSuspended`,
  `TestToggle_OverridesAutoSuspension`,
  `TestPersistentHotkey_FiresWhileSuspended`, `TestDefaultToggleHotkey`.

## [0.1.0] - 2026-04-28

Initial implementation of the macOS port. Establishes the full
architecture described in `DESIGN.md` and ships every keyboard shortcut
listed there with 1:1 functional parity vs. the legacy Hammerspoon setup.

### Added

- **Core interfaces** (`internal/wm/`): `Window`, `WindowManager`,
  `HotkeyManager`, `Rect`, `ScreenDirection`. Platform-agnostic, drives
  both the engine and the UI.
- **macOS platform layer** (`internal/platform/macos/`):
  - `window.go` — `AXUIElement` wrapper with focused-window lookup,
    frame get/set, native fullscreen toggle, and an accessibility-prompt
    helper.
  - `workspace.go` — `NSWorkspace` frontmost-app detection,
    multi-monitor screen geometry, NSScreen→AX coordinate conversion,
    proportional cross-screen translocation.
  - `hotkey.go` — Carbon `RegisterEventHotKey` registration with
    dynamic register/unregister on suspension so blocklisted apps
    receive the original keystrokes.
  - `keycodes.go` — case-insensitive key + modifier name resolver
    backed by macOS virtual keycodes.
- **Engine** (`internal/engine/`):
  - Action table with snap layouts (halves, thirds, fourths, quarters,
    bottom strips), maximize, native-fullscreen toggle, relative move
    and resize, screen-jump throws.
  - DRY action dispatcher reused by hotkeys and tray clicks.
  - Two-axis suspension state machine (manual toggle + automatic
    blocklist match) with a 500ms polling loop.
  - Hardcoded blocklist for Microsoft Remote Desktop, Windows App,
    Apple Screen Sharing, RealVNC, TigerVNC.
- **Menu-bar UI** (`internal/ui/`): icon swap on suspension, status
  readout of the current foreground app, Suspend/Activate toggle,
  click-to-run "Shortcuts" submenu grouped by category, Quit, version
  footer.
- **Embedded icons** (`internal/icon/`) generated by `scripts/gen-icon`,
  plus the multi-resolution `.iconset` consumed by `iconutil` for the
  `.app` bundle.
- **Build system** (`Makefile`): `build`, `run`, `test` (`-race`),
  `app`, `install`, `icons`, `version`, `version-increment`, `push`,
  plus a colorised `help` target.
- **App bundle**: `assets/Info.plist.template` with `LSUIElement=true`
  so GWiM runs as a menu-bar agent without a Dock icon.
- **Windows scaffold** (`internal/platform/windows/doc.go`,
  `cmd/gwim/main_windows.go`) with a clear "not yet implemented" stub
  pointing at PRD §4.4.
- **Tests** (`-race` clean):
  - `engine`: layout math, dispatcher suspension semantics, blocklist
    polling, tray-bypass behaviour, action-table validation.
  - `platform/macos`: cross-screen scaling math, screen-overlap helper,
    keycode and modifier resolution.

### Documentation

- README.md rewritten with feature overview, install/dev workflow, full
  shortcut table, project layout, and architecture notes.
- DESIGN.md left unchanged — implementation matches the PRD verbatim.

[Unreleased]: https://github.com/nealhardesty/gwim/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nealhardesty/gwim/releases/tag/v0.1.0
