//go:build windows

package engine

import "strings"

// formatShortcut renders a Shortcut using the Microsoft-standard
// "Ctrl+Alt+Shift+Win+Key" text form so the tray menu reads
// naturally for Windows users — that's the notation Windows itself
// uses throughout File Explorer, the Settings app, and Visual
// Studio's keybinding editor.
//
// Modifier order follows the Microsoft style guide:
//
//	Win → Ctrl → Alt → Shift → Key
//
// Duplicate aliases (cmd/command/win/meta all map to Win) are
// deduplicated so a shortcut declared with `["cmd", "ctrl"]` doesn't
// render as "Win+Ctrl+Win+…" if anyone later adds another alias.
func formatShortcut(s Shortcut) string {
	var hasWin, hasCtrl, hasAlt, hasShift bool
	for _, m := range s.Modifiers {
		switch m {
		case "ctrl", "control":
			hasCtrl = true
		case "alt", "option", "opt":
			hasAlt = true
		case "shift":
			hasShift = true
		case "cmd", "command", "win", "meta":
			hasWin = true
		}
	}
	parts := make([]string, 0, 5)
	if hasWin {
		parts = append(parts, "Win")
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

// keyDisplay returns the human-friendly label for a key name in
// Windows text form. Special keys use the names Microsoft documents
// (Enter, Esc, Tab, Backspace, Delete, Space) rather than macOS
// glyphs. Letters / digits / punctuation pass through upper-cased.
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
	case "return":
		return "Enter"
	case "enter":
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
