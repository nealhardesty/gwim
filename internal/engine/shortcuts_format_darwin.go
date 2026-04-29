//go:build darwin

package engine

// formatShortcut renders a Shortcut using the standard macOS glyph
// shorthand (⌃ ⌥ ⇧ ⌘) so the tray menu reads naturally — that's the
// notation Apple uses everywhere from menu bars to System Settings.
func formatShortcut(s Shortcut) string {
	out := ""
	for _, m := range s.Modifiers {
		switch m {
		case "ctrl", "control":
			out += "⌃"
		case "alt", "option", "opt":
			out += "⌥"
		case "shift":
			out += "⇧"
		case "cmd", "command", "win", "meta":
			out += "⌘"
		}
	}
	return out + keyDisplay(s.Key)
}

// keyDisplay returns the human-friendly label for a key name in macOS
// glyph form. Letters / digits / punctuation pass through upper-cased.
func keyDisplay(k string) string {
	switch k {
	case "left":
		return "←"
	case "right":
		return "→"
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "return":
		return "↩"
	case "enter":
		return "⌅"
	case "escape", "esc":
		return "⎋"
	case "tab":
		return "⇥"
	case "space":
		return "Space"
	case "delete":
		return "⌫"
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
