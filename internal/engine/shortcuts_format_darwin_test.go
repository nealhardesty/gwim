//go:build darwin

package engine

import "testing"

// TestFormatShortcut_Darwin pins the macOS glyph format the tray menu
// shows. Mirrors what Apple's HIG documents for menu accelerators —
// users are accustomed to ⌃⌥⇧⌘ from every other Mac app.
func TestFormatShortcut_Darwin(t *testing.T) {
	cases := []struct {
		mods []string
		key  string
		want string
	}{
		{[]string{"ctrl", "alt"}, "left", "⌃⌥←"},
		{[]string{"ctrl", "alt"}, "h", "⌃⌥H"},
		{[]string{"ctrl", "alt", "shift"}, "return", "⌃⌥⇧↩"},
		{[]string{"ctrl", "alt", "shift", "cmd"}, "k", "⌃⌥⇧⌘K"},
		{[]string{"ctrl", "alt"}, "tab", "⌃⌥⇥"},
		{[]string{"ctrl", "alt"}, "escape", "⌃⌥⎋"},
	}
	for _, c := range cases {
		got := formatShortcut(Shortcut{Modifiers: c.mods, Key: c.key})
		if got != c.want {
			t.Errorf("formatShortcut(%v,%q) = %q, want %q", c.mods, c.key, got, c.want)
		}
	}
}
