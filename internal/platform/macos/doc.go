// Package macos contains the macOS-specific implementations of the
// interfaces defined in internal/wm.
//
// The package uses cgo to bridge into three native frameworks:
//
//   - ApplicationServices / Accessibility (AXUIElement) – window manipulation.
//   - AppKit (NSWorkspace, NSScreen)                    – active-app & screen geometry.
//   - Carbon (RegisterEventHotKey)                       – global hotkey hooks.
//
// All exported constructors return concrete types that satisfy the wm.*
// interfaces. Platform selection is performed at compile time via build
// tags: every file in this package is gated by `//go:build darwin`.
//
// Threading model:
//
//   - The Carbon hotkey event handler is invoked on the main thread.
//     Handlers must be cheap; long-running work should be dispatched to
//     a goroutine.
//   - AX API calls are safe to invoke from any goroutine, but for
//     simplicity GWiM funnels them through the main goroutine where
//     possible.
package macos
