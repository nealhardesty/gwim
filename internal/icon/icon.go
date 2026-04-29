// Package icon provides the embedded menu-bar / system-tray icons used by
// the systray UI.
//
// We ship two variants — an "active" icon shown while GWiM is dispatching
// hotkeys, and a "suspended" icon shown while suspended from the menu or
// toggle shortcut — in whichever raster format the host platform's tray
// API expects:
//
//   - macOS (NSStatusItem) consumes a PNG. See [icon_darwin.go].
//   - Windows (Shell_NotifyIcon) consumes an ICO. See [icon_windows.go].
//
// The platform-tagged files declare the embedded byte slices (`active`,
// `suspended`); this file only exposes the small accessor surface so the
// systray UI doesn't need to know which format it's looking at. Both
// variants are generated procedurally at build time by
// `scripts/gen-icon`.
package icon

// Active returns the icon bytes for the active state, in the native format
// expected by the host platform's tray API (PNG on macOS, ICO on Windows).
func Active() []byte { return active }

// Suspended returns the icon bytes for the suspended state, in the native
// format expected by the host platform's tray API.
func Suspended() []byte { return suspended }
