// Package icon provides the embedded menu-bar icons used by the systray UI.
//
// We ship two PNG variants: an "active" icon shown while GWiM is dispatching
// hotkeys, and a "suspended" icon shown while suspended (manually or due to
// a blocklisted foreground app). Both are generated procedurally at compile
// time by scripts/gen-icon to avoid checking binary assets into git.
package icon

import (
	_ "embed"
)

//go:embed assets/icon-active.png
var active []byte

//go:embed assets/icon-suspended.png
var suspended []byte

// Active returns the PNG bytes for the active state.
func Active() []byte { return active }

// Suspended returns the PNG bytes for the suspended state.
func Suspended() []byte { return suspended }
