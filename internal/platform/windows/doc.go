// Package windows is the Windows port of GWiM (PRD §4.4). It implements
// the same wm.WindowManager and wm.HotkeyManager interfaces backed by
// pure Win32 (user32.dll / kernel32.dll) — no cgo required.
//
// File layout mirrors internal/platform/macos:
//
//   - keycodes.go     — VK_* / MOD_* tables and modifier-mask helpers.
//   - window.go       — winWindow + winWindowManager: SetWindowPos /
//     GetWindowRect / MonitorFromWindow + multi-monitor neighbor logic
//     and borderless-fullscreen toggle with per-HWND state stash.
//   - workspace.go    — active-app identifier (GetForegroundWindow ->
//     QueryFullProcessImageNameW returning the EXE basename).
//   - hotkey.go       — winHotkeyManager: dedicated OS-thread message
//     pump driving RegisterHotKey / UnregisterHotKey via WM_HOTKEY.
//     Cross-thread requests (register / suspend / quit) are delivered
//     through PostThreadMessageW.
//   - launchatlogin.go — Open at Login via the HKCU Run registry key,
//     so the user can opt into auto-start on login without admin rights.
//
// The Alt-Tab window switcher (internal/altswitch + macos/altswitch_native.m)
// is intentionally NOT ported — Windows already provides Alt+Tab natively.
//
// Threading: Win32 hotkey events fire on the queue of the thread that
// called RegisterHotKey, so winHotkeyManager owns a dedicated OS-thread-
// locked goroutine for the lifetime of the manager. The systray UI runs
// on the main thread (main_windows.go locks it before tray.Run).
package windows
