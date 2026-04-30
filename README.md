# GWiM — Golang Window Manager

A lightweight, keyboard-driven window manager for **macOS and Windows**,
written in Go. GWiM replaces a legacy Hammerspoon (Lua) setup with a single
compiled binary that ships as a menu-bar (macOS) or system-tray (Windows)
agent, registers global hotkeys, and lets you suspend them when needed so
shortcuts reach another app unchanged (e.g. remote desktop).

> **Status:** Both macOS and Windows are supported. The Windows port covers
> every shortcut except the Alt-Tab switcher (Windows already has its own
> Alt+Tab) and uses pure-Go `syscall` bindings to user32.dll / kernel32.dll
> — no cgo, no MinGW. See [`DESIGN.md`](DESIGN.md) §4.4.

---

## Features

- **Hotkey-driven layouts**: snap to halves, thirds, fourths, quarters, and
  bottom strips; relative move and resize; throw windows between monitors.
- **Native fullscreen toggle** via `Ctrl+Alt+F` (true Spaces fullscreen, not
  just frame maximization).
- **Manual suspension**: suspend hotkeys from the menu bar or **`Ctrl+Alt+X`**
  so keystrokes go to the foreground app (e.g. remote desktop). While suspended,
  GWiM physically unregisters its regular hotkeys; the toggle shortcut stays
  registered so you can resume anytime.
- **Persistent toggle** (`Ctrl+Alt+X`): flips GWiM on/off; works even while
  regular shortcuts are suspended so you can resume without using the menu bar.
- **Alt-Tab window switcher** (`⌥⇥` / `⌥⇧⇥`): per-window MRU switching
  across all running apps **and every macOS Space** — windows on other
  Spaces, including native-fullscreen Spaces, now show up alongside
  the windows on the currently-visible Space. Holding Option opens the
  same centred overlay on **every connected display** (each scaled to
  use up to **90%** of that screen's working area) with **live window
  thumbnails** plus an app-icon badge per slot. Slots are **grouped by
  Space**, with a small header label per group (`D1·S2`,
  `Space 1 · current`, `Sticky`, …) and a thin divider between groups;
  the focused window's Space leads, sticky (all-Space) windows trail.
  Repeated Tab / Shift+Tab cycles the highlight across all groups;
  **clicking a slot** commits and raises that window immediately.
  Releasing Option commits and raises the keyboard-highlighted window.
  Esc cancels. **Minimised windows and the windows of hidden apps
  (Cmd+H) are included** and rendered at reduced opacity so they're
  easy to recognise at a glance; committing to one un-hides the app
  and un-minimises the window automatically. Thumbnails use
  ScreenCaptureKit (macOS 14+) and require **Screen Recording**
  permission — when denied, the overlay degrades gracefully to
  icon-only. The same chord is also a clickable item in the tray's
  **Window Switcher** submenu (Return or **click a slot** commits, Esc
  cancels).
- **Menu-bar UI**:
  - One-click **Suspend / Activate** toggle.
  - **Shortcuts submenu** listing every action with its keyboard accelerator.
    Clicking any item runs the action on the currently active window — same
    code path as the hotkey, satisfying DRY.
  - Live status: foreground app, effective suspend mode, and
    **Accessibility grant state**.
  - Clickable **Accessibility** status row opens
    **System Settings → Privacy & Security → Accessibility** directly.
  - Clickable **Screen Recording** status row surfaces grant state. When
    thumbnails are off, the **first** click triggers the macOS permission
    dialog only; a **second** click opens **System Settings → Privacy &
    Security → Screen Recording** (so you are not hit with both at once).
    Required only for live thumbnails in the Alt-Tab switcher; switcher
    works without it (icon-only mode).
  - **Open at Login** (checkable): when you run from **GWiM.app** on
    **macOS 13 or later**, toggles registration with the system so GWiM
    starts at login (same mechanism as **System Settings → General →
    Login Items**). The item is omitted for a bare `gwim` binary or on
    older macOS.
  - Hidden **Last action error** row appears automatically if any action
    fails (for example, if macOS revokes Accessibility permission).
- **Single binary, ~3 MB**, no runtime dependencies beyond the macOS
  Accessibility API (macOS) or user32.dll / kernel32.dll (Windows).

### Differences on Windows

The same engine and shortcut table run on both platforms. A few items
shift to match Windows conventions:

- **No Alt-Tab switcher.** Windows already provides Alt+Tab natively, so
  the `⌥⇥` overlay is omitted on Windows builds.
- **`Ctrl+Alt+F` toggles borderless fullscreen** (strip window chrome,
  fill the monitor) instead of macOS Spaces fullscreen, which has no
  exact Windows equivalent. A second press restores the original frame
  and chrome.
- **`Cmd` aliases to the Windows key** in shortcut definitions, so
  `Ctrl+Alt+Cmd+H` (throw west) becomes Ctrl+Alt+Win+H. Some
  reserved Win+letter combos are pre-empted by Windows itself
  (e.g. Win+L locks); GWiM logs the failed registration but other
  hotkeys still bind.
- **Tray UI**: same Suspend toggle, Shortcuts submenu, Open at Login,
  and Quit. The Accessibility / Screen Recording rows are macOS-only
  and are hidden automatically on Windows (no equivalent permissions).
- **Shortcuts cheatsheet** in the tray uses Microsoft-standard text
  (`Ctrl+Alt+H`, `Win+Ctrl+Alt+Shift+K`, …) instead of the macOS
  glyphs (`⌃⌥H`) — same key chords, native notation per platform.
- **Open at Login** uses the standard
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` registry key
  pointing to the running executable — no admin rights required.

## Default keybindings

All snap shortcuts use **`⌃⌥`** (Ctrl+Alt). Move uses **`⌃⌥⇧`**, resize uses
**`⌃⌥⇧⌘`**, screen jumping uses **`⌃⌥⌘`**.

| Layout                   | Key(s)              |
|--------------------------|---------------------|
| Snap left half           | `←` or `H`          |
| Snap right half          | `→` or `L`          |
| Snap top half            | `↑`                 |
| Snap bottom half         | `↓`                 |
| Snap quarters            | `U` `I` `J` `K`     |
| Snap thirds              | `1` `2` `3`         |
| Snap fourths             | `4` `5` `6` `7`     |
| Bottom strip (full)      | `M`                 |
| Bottom strip (left/right)| `N` / `,`           |
| Maximize (frame)         | `↩` (Return)        |
| Native fullscreen toggle | `F`                 |
| **Toggle GWiM on/off**   | **`X`** *(persistent — works while regular hotkeys are suspended)* |

The window switcher uses its own chord (no `⌃` modifier):

| Action                          | Keys             |
|---------------------------------|------------------|
| Open switcher (forward)         | `⌥⇥`             |
| Open switcher (backward)        | `⌥⇧⇥`            |
| Advance highlight while open    | `⇥` / `⇧⇥`       |
| Commit selection                | release `⌥`, click a slot, or `↩` (when opened from the tray) |
| Cancel                          | `⎋`              |

| Verb                     | Modifiers           | Keys                  |
|--------------------------|---------------------|-----------------------|
| Move 100px               | `⌃⌥⇧`               | `H/J/K/L` or arrows   |
| Resize 100px (W/H)       | `⌃⌥⇧⌘`              | `H/J/K/L` or arrows   |
| Throw to adjacent screen | `⌃⌥⌘`               | `H/J/K/L` or arrows   |

The full table lives in `internal/engine/shortcuts.go` and is the single
source of truth for both the hotkey registrar and the tray menu.

## Installation

### Option 1: `go install` (binary only)

If you have a Go toolchain, you can install the raw binary into `$GOBIN`
directly from the module proxy:

```bash
go install github.com/nealhardesty/gwim@latest
gwim                      # macOS / Linux: launch in the foreground
gwim.exe                  # Windows: launch in the foreground
```

On macOS this produces a working `gwim` binary but **does not** create the
`GWiM.app` bundle. Without the bundle the app still runs and registers
hotkeys, but every rebuild will invalidate macOS Accessibility
permission (TCC keys it on the codesign identity, which only the signed
bundle pins). Use this path for quick experimentation; use Option 2 for
a real install.

On Windows the resulting `gwim.exe` is fully functional on its own — pin
it via the tray's **Open at Login** menu item to start it on every login.

### Option 2: `make app` + `make install` (recommended on macOS)

```bash
git clone https://github.com/nealhardesty/gwim.git
cd gwim
make app                  # build dist/GWiM.app (ad-hoc signed)
make install              # copy to /Applications
open -a GWiM              # launch
```

On first launch, macOS will prompt for **Accessibility** permission. Grant
it in **System Settings → Privacy & Security → Accessibility**, then quit
and relaunch GWiM.

If hotkeys appear to do nothing after a rebuild/install, rebind permission:

1. Remove old `GWiM` entries from Accessibility.
2. Re-add `/Applications/GWiM.app` and toggle it ON.
3. Quit and relaunch GWiM.

`make app` now ad-hoc signs the `.app` bundle with a stable identifier so
TCC permission should persist across future rebuilds.

### Option 3: Windows binary

The canonical `Makefile` assumes a Unix toolchain (`awk`, `find`,
`mkdir -p`, …) that isn't on a stock Windows install. GWiM ships a
sibling `Makefile.windows` for native Windows builds — it uses only
`go` plus `cmd.exe` builtins and PowerShell, so it works from any
shell as long as Go is on PATH.

On Windows, with Go installed (<https://go.dev/dl/>):

```powershell
git clone https://github.com/nealhardesty/gwim.git
cd gwim
make -f Makefile.windows build      # produces dist\gwim.exe
.\dist\gwim.exe                     # launch — tray icon appears
```

Other Windows-side targets (same names as the *nix Makefile where they
apply): `test`, `vet`, `fmt`, `tidy`, `check`, `icons`, `clean`,
`version`, `deps`. Run `make -f Makefile.windows help` for the full
list. macOS-only targets (`app`, `codesign`, `install`, `release`,
`push`) are deliberately absent because they don't apply on Windows.

> If you have Git for Windows / MSYS2 set up and prefer the regular
> Makefile, that works too — Git Bash provides the Unix tools the
> canonical Makefile depends on. `Makefile.windows` is the
> tools-free fallback.

Or cross-compile from macOS / Linux:

```bash
make build-windows        # produces dist/gwim.exe (CGO disabled)
```

Once `gwim.exe` is running, open the tray menu and tick **Open at Login**
to start it on every login (writes the standard
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` registry value).
No admin rights, no installer.

## Development

```bash
# *nix Makefile (macOS / Linux / Git Bash)
make help                 # list every target
make build                # binary -> dist/gwim (host platform)
make build-windows        # cross-compile -> dist/gwim.exe (CGO_ENABLED=0)
make run                  # foreground run with stdout logging
make run-app              # launch dist/GWiM.app like a real install
make test                 # tests with -race
make check                # fmt + vet + test (pre-commit gate)
make app                  # full .app bundle
make codesign             # ad-hoc sign dist/GWiM.app (run by make app)
make install              # install signed app into /Applications
make icons                # regenerate tray PNGs / ICOs and the macOS .iconset

# Windows-native Makefile (vanilla cmd / PowerShell, no awk/find/mkdir-p)
make -f Makefile.windows help       # list Windows targets
make -f Makefile.windows build      # binary -> dist\gwim.exe
make -f Makefile.windows test       # go test -race ./...
make -f Makefile.windows icons      # regenerate tray PNGs / ICOs
```

### Project layout

```
main.go, main_darwin.go,  # entrypoint + per-OS bootstrap (lives at module root
main_windows.go,          # so `go install github.com/nealhardesty/gwim@latest` works)
version.go
internal/wm/              # platform-agnostic interfaces (Window, WindowManager, HotkeyManager, Switcher)
internal/altswitch/       # MRU stash + per-Space group sort for the Alt-Tab switcher (macOS-only feature; platform-agnostic logic)
internal/platform/macos/  # cgo bridge: AXUIElement, NSWorkspace, Carbon hotkeys, CGEventTap, NSWindow overlay
internal/platform/windows/# pure-Go Win32 bindings: SetWindowPos, RegisterHotKey + WM_HOTKEY pump, registry login
internal/engine/          # action table, layout math, suspension dispatcher
internal/ui/              # systray menu UI (cross-platform via getlantern/systray)
internal/icon/            # embedded tray PNGs (macOS) and ICOs (Windows); committed for //go:embed
scripts/gen-icon/         # regenerates the embedded icons + .iconset (run via `make icons`)
assets/Info.plist.template# .app bundle plist (LSUIElement=YES)
```

### Architecture highlights

- **Interface-first**: `internal/engine` and `internal/ui` know nothing
  about the host OS. Both `internal/platform/macos` (cgo + Cocoa /
  Carbon / AX) and `internal/platform/windows` (pure-Go user32.dll /
  kernel32.dll bindings) implement the same `wm.WindowManager` and
  `wm.HotkeyManager` interfaces.
- **Suspension is a state machine** driven by `UserMode` (Auto / ForceActive /
  ForceSuspended) from the menu or **`Ctrl+Alt+X`**. A background poller
  refreshes Accessibility and Screen Recording grant state for the tray; it
  does not change whether hotkeys are active.

  When the engine is suspended, regular hotkeys are physically unregistered
  so the OS dispatches them to the foreground app. The Ctrl+Alt+X toggle is
  registered as a **persistent** hotkey (`HotkeyManager.RegisterPersistent`)
  so it stays bound at all times and provides a way to resume.
- **Accessibility health is first-class state**: the engine polls a
  non-prompting accessibility check and surfaces the result to the tray.
  Clicking the tray row re-checks immediately and opens System Settings.
- **Build/install lifecycle is TCC-safe**: `make app` signs the bundle
  (`make codesign`) so the app has a stable identity (`dev.nealhardesty.gwim`)
  and macOS Accessibility grants remain valid across rebuilds.
- **Chromium / Electron compatibility**: Chrome, Slack, Edge, Brave,
  VS Code, Discord, and similar apps require a stack of AX
  workarounds in `internal/platform/macos/window.go`.
  1. **Two-path focused-window resolution.** On macOS 26 (Tahoe)
     the system-wide AX element refuses to identify Chromium apps as
     the focused application (returns `kAXErrorCannotComplete`).
     `gwim_ax_focused_window` falls back to
     `NSWorkspace.frontmostApplication.processIdentifier` →
     `AXUIElementCreateApplication(pid)` — same approach AltTab.app
     and Yabai use.
  2. **`AXManualAccessibility` opt-in.** Since Chromium 88 the
     renderer's AX tree is opt-in. The focused-window helper writes
     `AXManualAccessibility = true` on the application element when
     `kAXFocusedWindow` comes back empty, polls briefly for the
     bridge to come up, and retries.
  3. **`AXEnhancedUserInterface` toggle.** `gwim_ax_set_frame`
     applies the Hammerspoon `setFrameCorrectness` trick: temporarily
     toggle the attribute off, write `position → size → position`,
     restore. A read-back / retry step covers apps that revert
     geometry on first write.

  Set `GWIM_AX_DEBUG=1` in the launch environment to stream per-call
  AX diagnostics to stderr when investigating app-specific issues.
- **Tray clicks bypass suspension** because they are an explicit user
  request. Hotkeys observe suspension because they are ambiguous intent.
- **Single-source shortcut table**: `engine.DefaultShortcuts()` powers
  both the OS-level hotkey registration loop and the menu's accelerator
  labels — they cannot drift out of sync.

## Versioning & releases

The current version lives in [`version.go`](version.go).

Per the project conventions (see [`AGENTS.md`](AGENTS.md)) all commits
must go through `make push`, which:

1. Runs `fmt + vet + test`.
2. Bumps the patch version (override with `BUMP=major|minor|patch`).
3. Rebuilds the `.app` bundle.
4. Commits, pushes, tags, and pushes the tag.

`make push` is the **only** sanctioned commit/publish path — never invoke
`git add/commit/push` directly.

After a version tag exists on GitHub, **`make release`** bundles BOTH
platforms in one shot:

- Builds the macOS `.app`, zips it with `ditto` to
  `dist/GWiM-<version>.zip`.
- Cross-builds the Windows binary (`CGO_ENABLED=0`,
  `GOOS=windows GOARCH=amd64`) and copies it to a versioned
  `dist/gwim-<version>.exe`.
- Uses the [GitHub CLI](https://cli.github.com/) (`gh`) to create the
  matching GitHub Release for that tag — or upload-with-`--clobber` if
  the release already exists — attaching both assets.

Run `gh auth login` once per machine. The tag must already exist
locally (e.g. via `make push`).

## Changelog

See [`CHANGELOG.md`](CHANGELOG.md).

## License

MIT — see [`LICENSE`](LICENSE).
