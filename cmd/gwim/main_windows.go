//go:build windows

package main

import "errors"

// startApp on Windows is intentionally a stub. The Windows port (PRD §4.4)
// is fast-follow work; this file exists so cross-compiling for Windows
// produces a clean "not yet implemented" message rather than a missing-
// symbol link error. The platform layer will live in
// internal/platform/windows once implemented.
func startApp() error {
	return errors.New("Windows port not yet implemented; see DESIGN.md §4.4")
}
