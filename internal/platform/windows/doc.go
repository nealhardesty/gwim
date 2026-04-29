// Package windows is reserved for the Windows port of GWiM (PRD §4.4).
//
// The architecture mirrors internal/platform/macos:
//
//   - Window manipulation via golang.org/x/sys/windows + user32.dll
//     (SetWindowPos, GetWindowRect, SetForegroundWindow).
//   - Active-app detection via GetForegroundWindow,
//     GetWindowThreadProcessId, QueryFullProcessImageNameW (the executable
//     base name fills the role of macOS bundle ID in the engine blocklist).
//   - Global hotkeys via RegisterHotKey / UnregisterHotKey from user32.dll
//     with a hidden message-only window pumping WM_HOTKEY.
//
// No source files exist in this package yet — when the port begins, add
// `//go:build windows` files implementing wm.WindowManager and
// wm.HotkeyManager and wire them in main_windows.go at the module root.
package windows
