//go:build !darwin && !windows

package icon

import (
	_ "embed"
)

// Fallback for platforms outside our two officially supported targets
// (darwin / windows). The PNG variant compiles fine on Linux and lets
// `go vet ./...` / `go test ./...` stay green for cross-platform CI
// even though the `main` package itself has no `startApp` for these
// platforms.

//go:embed assets/icon-active.png
var active []byte

//go:embed assets/icon-suspended.png
var suspended []byte
