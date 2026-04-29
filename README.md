# GWiM — Golang Window Manager

A lightweight, keyboard-driven window manager for macOS, written in Go. GWiM
replaces a legacy Hammerspoon (Lua) setup with a single compiled binary that
ships as a menu-bar agent, registers global hotkeys, and yields control to
remote-desktop clients automatically.

> **Status:** macOS implementation is complete. Windows is scaffolded
> (`internal/platform/windows/`) and is the next milestone — see
> [`DESIGN.md`](DESIGN.md) §4.4.

---

## Features

- **Hotkey-driven layouts**: snap to halves, thirds, fourths, quarters, and
  bottom strips; relative move and resize; throw windows between monitors.
- **Native fullscreen toggle** via `Ctrl+Alt+F` (true Spaces fullscreen, not
  just frame maximization).
- **Context-aware suspension**: when a remote-desktop or VNC app gains focus
  (Microsoft Remote Desktop, Apple Screen Sharing, RealVNC, TigerVNC,
  Windows App), GWiM physically unregisters its hotkeys so the keystrokes
  reach the remote machine.
- **Always-available toggle** (`Ctrl+Alt+X`): a persistent hotkey that
  flips GWiM on/off and can **override auto-suspension**, so you can
  enable GWiM mid-screen-sharing if you need to rearrange local windows
  without dismissing the remote session. The same toggle is on the menu
  bar.
- **Alt-Tab window switcher** (`⌥⇥` / `⌥⇧⇥`): per-window MRU switching
  across all running apps. Holding Option opens a centred overlay with
  **live window thumbnails** plus an app-icon badge per slot; repeated
  Tab / Shift+Tab cycles the highlight; releasing Option commits and
  raises the chosen window. Esc cancels. Thumbnails use ScreenCaptureKit
  (macOS 14+) and require **Screen Recording** permission — when denied,
  the overlay degrades gracefully to icon-only. The same chord is also
  a clickable item in the tray's **Window Switcher** submenu (Return
  commits, Esc cancels). See [`ALTTAB.md`](ALTTAB.md).
- **Menu-bar UI**:
  - One-click **Suspend / Activate** toggle.
  - **Shortcuts submenu** listing every action with its keyboard accelerator.
    Clicking any item runs the action on the currently active window — same
    code path as the hotkey, satisfying DRY.
  - Live status: foreground app, effective suspend mode, and
    **Accessibility grant state**.
  - Clickable **Accessibility** status row opens
    **System Settings → Privacy & Security → Accessibility** directly.
  - Clickable **Screen Recording** status row mirrors the AX one —
    surfaces grant state and triggers the macOS permission prompt /
    System Settings on click. Required only for live thumbnails in
    the Alt-Tab switcher; switcher works without it (icon-only mode).
  - **Open at Login** (checkable): when you run from **GWiM.app** on
    **macOS 13 or later**, toggles registration with the system so GWiM
    starts at login (same mechanism as **System Settings → General →
    Login Items**). The item is omitted for a bare `gwim` binary or on
    older macOS.
  - Hidden **Last action error** row appears automatically if any action
    fails (for example, if macOS revokes Accessibility permission).
- **Single binary, ~3 MB**, no runtime dependencies beyond the macOS
  Accessibility API.

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
| **Toggle GWiM on/off**   | **`X`** *(persistent — works even during screen sharing)* |

The window switcher uses its own chord (no `⌃` modifier):

| Action                          | Keys             |
|---------------------------------|------------------|
| Open switcher (forward)         | `⌥⇥`             |
| Open switcher (backward)        | `⌥⇧⇥`            |
| Advance highlight while open    | `⇥` / `⇧⇥`       |
| Commit selection                | release `⌥`, or `↩` (when opened from the tray) |
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
gwim                      # launch in the foreground
```

This produces a working `gwim` binary but **does not** create the
`GWiM.app` bundle. Without the bundle the app still runs and registers
hotkeys, but every rebuild will invalidate macOS Accessibility
permission (TCC keys it on the codesign identity, which only the signed
bundle pins). Use this path for quick experimentation; use Option 2 for
a real install.

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

## Development

```bash
make help                 # list every target
make build                # binary -> dist/gwim
make run                  # foreground run with stdout logging
make run-app              # launch dist/GWiM.app like a real install
make test                 # tests with -race
make check                # fmt + vet + test (pre-commit gate)
make app                  # full .app bundle
make codesign             # ad-hoc sign dist/GWiM.app (run by make app)
make install              # install signed app into /Applications
make icons                # regenerate menu-bar PNGs and .iconset
```

### Project layout

```
main.go, main_darwin.go,  # entrypoint + per-OS bootstrap (lives at module root
main_windows.go,          # so `go install github.com/nealhardesty/gwim@latest` works)
version.go
internal/wm/              # platform-agnostic interfaces (Window, WindowManager, HotkeyManager, Switcher)
internal/altswitch/       # MRU stash for the Alt-Tab switcher (platform-agnostic)
internal/platform/macos/  # cgo bridge: AXUIElement, NSWorkspace, Carbon hotkeys, CGEventTap, NSWindow overlay
internal/platform/windows/# (scaffold for Win32 port, see DESIGN.md §4.4)
internal/engine/          # action table, layout math, suspension dispatcher
internal/ui/              # systray menu UI
internal/icon/            # embedded menu-bar PNGs (committed; required by //go:embed)
scripts/gen-icon/         # regenerates the embedded icons + .iconset (run via `make icons`)
assets/Info.plist.template# .app bundle plist (LSUIElement=YES)
```

### Architecture highlights

- **Interface-first**: `internal/engine` and `internal/ui` know nothing
  about macOS. The Windows port plugs into the same interfaces in
  `internal/platform/windows/`.
- **Suspension is a state machine** with two inputs:
  - `UserMode` (Auto / ForceActive / ForceSuspended) — explicit user
    override that always wins when set to a Force value.
  - `autoSuspended` — driven by a 500ms blocklist poller.

  When the engine is effectively suspended, regular hotkeys are
  physically unregistered so the OS dispatches them to the foreground
  app. The Ctrl+Alt+X toggle is registered as a **persistent** hotkey
  (`HotkeyManager.RegisterPersistent`) so it stays bound at all times
  and gives the user a way back in.
- **Accessibility health is first-class state**: the engine polls a
  non-prompting accessibility check and surfaces the result to the tray.
  Clicking the tray row re-checks immediately and opens System Settings.
- **Build/install lifecycle is TCC-safe**: `make app` signs the bundle
  (`make codesign`) so the app has a stable identity (`dev.nealhardesty.gwim`)
  and macOS Accessibility grants remain valid across rebuilds.
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

After a version tag exists on GitHub, **`make release`** builds the
`.app`, zips it (`dist/GWiM-<version>.zip`), and uses the
[GitHub CLI](https://cli.github.com/) (`gh`) to create or update the
matching GitHub Release for that tag. Run `gh auth login` once per
machine.

## Changelog

See [`CHANGELOG.md`](CHANGELOG.md).

## License

MIT — see [`LICENSE`](LICENSE).
