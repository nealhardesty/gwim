//go:build darwin

package macos

// This file declares the cgo build flags shared across the macOS platform
// package. Every other cgo file in this package can rely on these:
//
//   - `-x objective-c` lets us write Objective-C inline in cgo preambles
//     (NSWorkspace, NSScreen, NSRunningApplication).
//   - `-fobjc-arc` enables Automatic Reference Counting so Objective-C
//     objects are released without manual `[obj release]` calls.
//   - The linker pulls in Cocoa (NS*), ApplicationServices (AXUIElement),
//     Carbon (RegisterEventHotKey, GetEventParameter), and ServiceManagement
//     (SMAppService login-item registration).
//
// Cgo merges flags from every file in the package, so declaring them in
// one place keeps the per-feature files focused on their preamble code.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -framework Carbon -framework ServiceManagement
*/
import "C"
