//go:build darwin

package macos

import (
	"strings"

	"github.com/nealhardesty/gwim/internal/wm"
)

// macOS virtual keycodes (see HIToolbox/Events.h kVK_* constants).
//
// Only the subset GWiM actually exposes is mapped. New shortcuts MUST add
// their key here; missing entries return wm.ErrUnsupportedKey at registration
// time so problems surface during Engine startup rather than at runtime.
var keycodeTable = map[string]uint32{
	// Letters (kVK_ANSI_*)
	"a": 0x00, "b": 0x0B, "c": 0x08, "d": 0x02, "e": 0x0E, "f": 0x03,
	"g": 0x05, "h": 0x04, "i": 0x22, "j": 0x26, "k": 0x28, "l": 0x25,
	"m": 0x2E, "n": 0x2D, "o": 0x1F, "p": 0x23, "q": 0x0C, "r": 0x0F,
	"s": 0x01, "t": 0x11, "u": 0x20, "v": 0x09, "w": 0x0D, "x": 0x07,
	"y": 0x10, "z": 0x06,

	// Number row (kVK_ANSI_<n>)
	"1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15, "5": 0x17,
	"6": 0x16, "7": 0x1A, "8": 0x1C, "9": 0x19, "0": 0x1D,

	// Punctuation
	",": 0x2B, ".": 0x2F, "/": 0x2C, ";": 0x29, "'": 0x27,
	"[": 0x21, "]": 0x1E, "\\": 0x2A, "`": 0x32, "-": 0x1B, "=": 0x18,

	// Navigation
	"return": 0x24, "enter": 0x4C, "tab": 0x30, "space": 0x31,
	"escape": 0x35, "esc": 0x35, "delete": 0x33, "forwarddelete": 0x75,

	// Arrow keys
	"left": 0x7B, "right": 0x7C, "down": 0x7D, "up": 0x7E,

	// Function row
	"f1": 0x7A, "f2": 0x78, "f3": 0x63, "f4": 0x76, "f5": 0x60,
	"f6": 0x61, "f7": 0x62, "f8": 0x64, "f9": 0x65, "f10": 0x6D,
	"f11": 0x67, "f12": 0x6F,
}

// Carbon modifier flags (Events.h):
//
//	cmdKey     = 1 << 8  (256)
//	shiftKey   = 1 << 9  (512)
//	optionKey  = 1 << 11 (2048)
//	controlKey = 1 << 12 (4096)
const (
	carbonCmd   uint32 = 1 << 8
	carbonShift uint32 = 1 << 9
	carbonAlt   uint32 = 1 << 11
	carbonCtrl  uint32 = 1 << 12
)

var modifierTable = map[string]uint32{
	"cmd":     carbonCmd,
	"command": carbonCmd,
	"win":     carbonCmd, // Windows-style alias for cross-platform shortcut definitions
	"meta":    carbonCmd,
	"shift":   carbonShift,
	"alt":     carbonAlt,
	"option":  carbonAlt,
	"opt":     carbonAlt,
	"ctrl":    carbonCtrl,
	"control": carbonCtrl,
}

// keycodeFor resolves a key name (case-insensitive) to its macOS virtual keycode.
func keycodeFor(name string) (uint32, error) {
	if code, ok := keycodeTable[strings.ToLower(strings.TrimSpace(name))]; ok {
		return code, nil
	}
	return 0, wm.ErrUnsupportedKey
}

// modifierMaskFor resolves a slice of modifier names to a packed Carbon mask.
func modifierMaskFor(mods []string) (uint32, error) {
	var mask uint32
	for _, m := range mods {
		bit, ok := modifierTable[strings.ToLower(strings.TrimSpace(m))]
		if !ok {
			return 0, wm.ErrUnsupportedModifier
		}
		mask |= bit
	}
	return mask, nil
}
