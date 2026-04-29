//go:build windows

package engine

import "testing"

// TestFormatShortcut_Windows pins the Microsoft-style accelerator text
// the tray menu shows on Windows. Order is Win→Ctrl→Alt→Shift→Key per
// the Windows style guide so it matches what users see in File
// Explorer's right-click menu, the Settings app, etc.
func TestFormatShortcut_Windows(t *testing.T) {
	cases := []struct {
		mods []string
		key  string
		want string
	}{
		{[]string{"ctrl", "alt"}, "left", "Ctrl+Alt+Left"},
		{[]string{"ctrl", "alt"}, "h", "Ctrl+Alt+H"},
		{[]string{"ctrl", "alt", "shift"}, "return", "Ctrl+Alt+Shift+Enter"},
		// `cmd` aliases to Win and renders FIRST per Microsoft style.
		{[]string{"ctrl", "alt", "shift", "cmd"}, "k", "Win+Ctrl+Alt+Shift+K"},
		{[]string{"ctrl", "alt"}, "tab", "Ctrl+Alt+Tab"},
		{[]string{"ctrl", "alt"}, "escape", "Ctrl+Alt+Esc"},
		// Toggle hotkey rendered as Ctrl+Alt+X (used as the Suspend
		// menu-item label and the Open at Login tooltip).
		{[]string{"ctrl", "alt"}, "x", "Ctrl+Alt+X"},
	}
	for _, c := range cases {
		got := formatShortcut(Shortcut{Modifiers: c.mods, Key: c.key})
		if got != c.want {
			t.Errorf("formatShortcut(%v,%q) = %q, want %q", c.mods, c.key, got, c.want)
		}
	}
}

// TestFormatShortcut_AliasDedup ensures double-listed modifier aliases
// don't produce duplicate "Win+Win+…" output.
func TestFormatShortcut_AliasDedup(t *testing.T) {
	got := formatShortcut(Shortcut{Modifiers: []string{"cmd", "win", "meta"}, Key: "h"})
	if got != "Win+H" {
		t.Errorf("alias dedup failed: got %q want Win+H", got)
	}
}
