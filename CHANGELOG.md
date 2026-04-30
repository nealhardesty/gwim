# Changelog

All notable changes to **GWiM** are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- **`make release` now bundles BOTH platforms.** The release target now
  depends on `app` *and* `build-windows`, so a single invocation
  produces the macOS `.app` zip (`dist/GWiM-<version>.zip`) **and** a
  versioned Windows binary (`dist/gwim-<version>.exe`), then attaches
  both as assets to the GitHub Release for `v<version>`. Previously the
  target only shipped the macOS `.app`. Also fixed a pre-existing
  Makefile bug where `RELEASE_ZIP := …$(VERSION)…` was defined before
  `VERSION` was set, causing the asset to be uploaded as
  `GWiM-.zip` (empty version segment); the artifact variables now sit
  after `VERSION` so `:=` immediate expansion picks up the resolved
  value.

- **Alt-Tab switcher now lists every standard window**, including
  **minimised windows** (previously skipped as an MVP punt) and the
  windows of **hidden (Cmd+H) apps**. Slots for those windows are drawn
  at reduced opacity (≈45%) so users can still distinguish them from
  on-screen windows at a glance. `gwim_raise_window` now un-hides the
  owning `NSRunningApplication` and clears `kAXMinimizedAttribute`
  before issuing `kAXRaiseAction`, retrying the AX lookup briefly while
  the AX tree settles after un-hide. For hidden apps where AX returns
  zero windows, the enumerator falls back to
  `CGWindowListCopyWindowInfo(kCGWindowListOptionAll, …)` filtered by
  pid + layer 0 + non-empty bounds so those windows still appear in
  the overlay. New `wm.WindowInfo.Minimized` and `wm.WindowInfo.Hidden`
  fields plumb the state through to the native overlay.

### Added

- **Windows port** (`internal/platform/windows/`). GWiM now runs on
  Windows 10 / 11 with full feature parity except the Alt-Tab switcher
  (Windows already has its own Alt+Tab). Pure-Go `syscall` bindings to
  `user32.dll` / `kernel32.dll` — no cgo, no MinGW. New files:
  - `keycodes.go` — VK_* / MOD_* tables; `cmd` / `win` / `meta` all
    alias to `MOD_WIN` so the cross-platform shortcut table works
    unchanged. `MOD_NOREPEAT` is OR'd into every binding so a held key
    doesn't fire repeatedly.
  - `window.go` — `winWindow` (`SetWindowPos` / `GetWindowRect` /
    `IsZoomed` / `ShowWindow`) and `winWindowManager` with multi-monitor
    enumeration via `EnumDisplayMonitors` + `MonitorFromWindow`. The
    `MoveWindowToScreen` neighbor logic is shared verbatim with the
    macOS implementation since both platforms use top-left-origin rects.
  - `workspace.go` — active-app identifier via `GetForegroundWindow` →
    `GetWindowThreadProcessId` → `OpenProcess` → `QueryFullProcessImageNameW`,
    returning the EXE basename (e.g. `chrome.exe`).
  - `hotkey.go` — `winHotkeyManager` with a dedicated OS-thread message
    pump driving `RegisterHotKey` / `UnregisterHotKey`. Cross-thread
    register / suspend / quit requests flow through `PostThreadMessageW`.
    Persistent vs regular split mirrors macOS (Ctrl+Alt+X stays bound
    while regular hotkeys are suspended).
  - `launchatlogin.go` — Open at Login via the standard
    `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` registry key
    (no admin rights). Toggleable from the tray.
  - `Ctrl+Alt+F` toggles **borderless fullscreen** on Windows (strip
    `WS_OVERLAPPEDWINDOW`, fill containing monitor; restore on toggle-off).
- **Windows ICO tray icons.** `scripts/gen-icon` derives
  `internal/icon/assets/icon-{active,suspended}.ico` from the
  hand-drawn `icon-{active,suspended}.png` source assets by wrapping
  the PNG payload verbatim in a single-entry PNG-in-ICO container —
  the menu-bar PNGs are now tracked source files and the build never
  rewrites them. The icon package uses build-tagged embed files so
  macOS gets PNGs and Windows gets ICOs through the same `Active()` /
  `Suspended()` API. A stdlib-only `scripts/bootstrap_ico.py` mirror
  exists for refreshing the ICOs without a Go install (e.g. when only
  the artwork has changed).
- **`make build-windows` Makefile target** for cross-compiling a Windows
  `.exe` from any host (CGO disabled). Supplements the existing macOS
  bundle pipeline.
- **Native cheatsheet notation per platform.** The tray's Shortcuts
  submenu and the Suspend item's accelerator label now render in the
  host's native style: macOS glyphs (`⌃⌥H`, `⌃⌥⇧↩`) on darwin,
  Microsoft-standard text (`Ctrl+Alt+H`, `Ctrl+Alt+Shift+Enter`) on
  Windows. `formatShortcut` and `keyDisplay` were split into
  `internal/engine/shortcuts_format_{darwin,windows,other}.go` via
  build tags; the platform-agnostic `PrimaryShortcutFor` /
  `ToggleHotkey.Format` API is unchanged. Modifier order on Windows
  follows the Microsoft style guide (Win→Ctrl→Alt→Shift→Key).
- **`Makefile.windows`** — sibling build file for native Windows hosts
  that lack the Unix toolchain (`awk` / `find` / `mkdir -p` / …) the
  canonical Makefile assumes. Uses cmd builtins + PowerShell so a
  vanilla Windows install with Go on PATH can run `make -f
  Makefile.windows build` / `test` / `icons` / `clean` / etc. without
  setting up Git Bash or MSYS2 first. The macOS Makefile is
  deliberately untouched.

### Changed

- **`internal/icon` split by build tag**: `icon.go` exposes the API only;
  the embedded byte slices live in `icon_darwin.go` (PNG),
  `icon_windows.go` (ICO), and an `icon_other.go` PNG fallback for any
  third-party platform.
- **Tray hides the Accessibility row when no probe is configured.** The
  `internal/ui/tray.go` `refresh()` path now hides the Accessibility row
  when `SuspensionState.AccessibilityChecked` is false (Windows builds)
  rather than displaying a misleading `"Accessibility: (unknown)"` label.
  macOS always supplies the probe, so this is a no-op there.
- **`golang.org/x/sys` bumped from `v0.1.0` to `v0.30.0`.** Required so
  `golang.org/x/sys/windows/registry` is available for the Open-at-Login
  helper. macOS build is unaffected.

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
