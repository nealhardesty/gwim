//go:build windows

package windows

import (
	"errors"
	"testing"

	"github.com/nealhardesty/gwim/internal/wm"
)

// TestKeycodeFor pins the subset of VK_* values the engine relies on. If
// we ever drop or remap a keycode that's bound in shortcuts.go, this test
// catches it before runtime would.
func TestKeycodeFor(t *testing.T) {
	tests := []struct {
		name string
		want uint32
	}{
		{"h", 0x48}, {"j", 0x4A}, {"k", 0x4B}, {"l", 0x4C},
		{"left", 0x25}, {"right", 0x27}, {"up", 0x26}, {"down", 0x28},
		{"return", 0x0D}, {"1", 0x31}, {",", 0xBC},
		{"H", 0x48}, // case-insensitive
	}
	for _, tc := range tests {
		got, err := keycodeFor(tc.name)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got 0x%X want 0x%X", tc.name, got, tc.want)
		}
	}
	if _, err := keycodeFor("notakey"); !errors.Is(err, wm.ErrUnsupportedKey) {
		t.Errorf("expected ErrUnsupportedKey, got %v", err)
	}
}

// TestModifierMaskFor pins the four-modifier mapping. Updates to the
// table must keep these bits stable or registered hotkeys break.
//
// MOD_NOREPEAT is OR'd into every result so we always assert against
// (mods | winModNoRepeat).
func TestModifierMaskFor(t *testing.T) {
	tests := []struct {
		name string
		mods []string
		want uint32
	}{
		{"ctrl-alt", []string{"ctrl", "alt"}, winModCtrl | winModAlt | winModNoRepeat},
		{"ctrl-alt-shift", []string{"ctrl", "alt", "shift"}, winModCtrl | winModAlt | winModShift | winModNoRepeat},
		{"ctrl-alt-shift-cmd", []string{"ctrl", "alt", "shift", "cmd"}, winModCtrl | winModAlt | winModShift | winModWin | winModNoRepeat},
		// "cmd" / "win" / "meta" all alias to MOD_WIN per DESIGN.md §3.5.
		{"alias-cmd", []string{"cmd"}, winModWin | winModNoRepeat},
		{"alias-win", []string{"win"}, winModWin | winModNoRepeat},
		{"alias-meta", []string{"meta"}, winModWin | winModNoRepeat},
		{"alias-option", []string{"option"}, winModAlt | winModNoRepeat},
		{"empty", nil, winModNoRepeat},
	}
	for _, tc := range tests {
		got, err := modifierMaskFor(tc.mods)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %#x want %#x", tc.name, got, tc.want)
		}
	}
	if _, err := modifierMaskFor([]string{"hyper"}); !errors.Is(err, wm.ErrUnsupportedModifier) {
		t.Errorf("expected ErrUnsupportedModifier, got %v", err)
	}
}
