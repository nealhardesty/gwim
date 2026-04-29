//go:build !darwin && !windows

package engine

import "strings"

// formatShortcut on non-darwin / non-windows targets uses the same
// pure-ASCII format as Windows (Ctrl+Alt+Shift+Meta+Key) — it's the
// most universally readable convention and matches what Linux desktop
// environments tend to display in their hotkey editors.
//
// The `main` package has no `startApp` for these targets so the
// fallback only kicks in for `go vet ./...` / `go test ./...` runs on
// CI Linux machines.
func formatShortcut(s Shortcut) string {
	var hasMeta, hasCtrl, hasAlt, hasShift bool
	for _, m := range s.Modifiers {
		switch m {
		case "ctrl", "control":
			hasCtrl = true
		case "alt", "option", "opt":
			hasAlt = true
		case "shift":
			hasShift = true
		case "cmd", "command", "win", "meta":
			hasMeta = true
		}
	}
	parts := make([]string, 0, 5)
	if hasMeta {
		parts = append(parts, "Meta")
	}
	if hasCtrl {
		parts = append(parts, "Ctrl")
	}
	if hasAlt {
		parts = append(parts, "Alt")
	}
	if hasShift {
		parts = append(parts, "Shift")
	}
	parts = append(parts, keyDisplay(s.Key))
	return strings.Join(parts, "+")
}

func keyDisplay(k string) string {
	switch k {
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "up":
		return "Up"
	case "down":
		return "Down"
	case "return", "enter":
		return "Enter"
	case "escape", "esc":
		return "Esc"
	case "tab":
		return "Tab"
	case "space":
		return "Space"
	case "delete":
		return "Delete"
	case "backspace":
		return "Backspace"
	}
	if len(k) == 1 {
		c := k[0]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		return string(c)
	}
	return k
}
