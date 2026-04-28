//go:build darwin

package macos

import (
	"errors"
	"testing"

	"github.com/nealhardesty/gwim/internal/wm"
)

// TestKeycodeFor pins the subset of keycodes the engine relies on. If we
// ever drop or remap a keycode that's bound in shortcuts.go, this test
// catches it before runtime would.
func TestKeycodeFor(t *testing.T) {
	tests := []struct {
		name string
		want uint32
	}{
		{"h", 0x04}, {"j", 0x26}, {"k", 0x28}, {"l", 0x25},
		{"left", 0x7B}, {"right", 0x7C}, {"up", 0x7E}, {"down", 0x7D},
		{"return", 0x24}, {"1", 0x12}, {",", 0x2B},
		{"H", 0x04}, // case-insensitive
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
func TestModifierMaskFor(t *testing.T) {
	tests := []struct {
		name string
		mods []string
		want uint32
	}{
		{"ctrl-alt", []string{"ctrl", "alt"}, carbonCtrl | carbonAlt},
		{"ctrl-alt-shift", []string{"ctrl", "alt", "shift"}, carbonCtrl | carbonAlt | carbonShift},
		{"ctrl-alt-shift-cmd", []string{"ctrl", "alt", "shift", "cmd"}, carbonCtrl | carbonAlt | carbonShift | carbonCmd},
		{"alias-option-win", []string{"option", "win"}, carbonAlt | carbonCmd},
		{"empty", nil, 0},
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
