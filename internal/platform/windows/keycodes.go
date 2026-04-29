//go:build windows

package windows

import (
	"strings"

	"github.com/nealhardesty/gwim/internal/wm"
)

// Win32 virtual-key codes (subset GWiM actually exposes). Names and values
// come from <winuser.h>. Anything not in this table returns
// wm.ErrUnsupportedKey at registration time so missing key mappings surface
// during engine startup rather than at first hotkey press.
//
// We mirror the macOS keycodes table key-for-key so the cross-platform
// shortcut definitions in internal/engine/shortcuts.go work unchanged on
// Windows.
var keycodeTable = map[string]uint32{
	// Letters (VK_A..VK_Z share ASCII values)
	"a": 0x41, "b": 0x42, "c": 0x43, "d": 0x44, "e": 0x45, "f": 0x46,
	"g": 0x47, "h": 0x48, "i": 0x49, "j": 0x4A, "k": 0x4B, "l": 0x4C,
	"m": 0x4D, "n": 0x4E, "o": 0x4F, "p": 0x50, "q": 0x51, "r": 0x52,
	"s": 0x53, "t": 0x54, "u": 0x55, "v": 0x56, "w": 0x57, "x": 0x58,
	"y": 0x59, "z": 0x5A,

	// Digit row (VK_0..VK_9 share ASCII values)
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,

	// OEM punctuation. The OEM codes vary by keyboard layout but the
	// US-ANSI mappings below match what Windows reports for the standard
	// 101/102-key layouts that all GWiM users will be on.
	",":  0xBC, // VK_OEM_COMMA
	".":  0xBE, // VK_OEM_PERIOD
	"/":  0xBF, // VK_OEM_2
	";":  0xBA, // VK_OEM_1
	"'":  0xDE, // VK_OEM_7
	"[":  0xDB, // VK_OEM_4
	"]":  0xDD, // VK_OEM_6
	"\\": 0xDC, // VK_OEM_5
	"`":  0xC0, // VK_OEM_3
	"-":  0xBD, // VK_OEM_MINUS
	"=":  0xBB, // VK_OEM_PLUS

	// Navigation
	"return": 0x0D, // VK_RETURN
	"enter":  0x0D, // VK_RETURN — Windows doesn't distinguish numpad enter for hotkeys
	"tab":    0x09, // VK_TAB
	"space":  0x20, // VK_SPACE
	"escape": 0x1B, // VK_ESCAPE
	"esc":    0x1B,
	"delete": 0x2E, // VK_DELETE
	// VK_BACK (backspace) intentionally omitted — macOS table treats
	// "delete" as backspace, but GWiM doesn't bind backspace so the
	// difference is moot.

	// Arrow keys
	"left":  0x25, // VK_LEFT
	"up":    0x26, // VK_UP
	"right": 0x27, // VK_RIGHT
	"down":  0x28, // VK_DOWN

	// Function row (VK_F1..VK_F12)
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74,
	"f6": 0x75, "f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79,
	"f11": 0x7A, "f12": 0x7B,
}

// Win32 RegisterHotKey modifier flags (winuser.h).
//
// MOD_NOREPEAT (0x4000) is OR'd into every binding so a held-down hotkey
// only fires once per keypress instead of repeating; the engine actions
// are not idempotent (relative move / resize accumulate) and a stuck Tab
// triggering 50 fires would teleport a window across the screen.
const (
	winModAlt      uint32 = 0x0001 // MOD_ALT
	winModCtrl     uint32 = 0x0002 // MOD_CONTROL
	winModShift    uint32 = 0x0004 // MOD_SHIFT
	winModWin      uint32 = 0x0008 // MOD_WIN — physical Windows / "Cmd" key
	winModNoRepeat uint32 = 0x4000 // MOD_NOREPEAT
)

// modifierTable maps the canonical GWiM modifier names (used in
// internal/engine/shortcuts.go) to Win32 MOD_* flags. The "cmd" / "win" /
// "meta" aliases all collapse to MOD_WIN — DESIGN.md §3.5 explicitly states
// "On Windows, Cmd = Win key", so the existing shortcut table works
// unchanged.
var modifierTable = map[string]uint32{
	"cmd":     winModWin,
	"command": winModWin,
	"win":     winModWin,
	"meta":    winModWin,
	"shift":   winModShift,
	"alt":     winModAlt,
	"option":  winModAlt,
	"opt":     winModAlt,
	"ctrl":    winModCtrl,
	"control": winModCtrl,
}

// keycodeFor resolves a key name (case-insensitive) to its Win32 virtual-key code.
func keycodeFor(name string) (uint32, error) {
	if code, ok := keycodeTable[strings.ToLower(strings.TrimSpace(name))]; ok {
		return code, nil
	}
	return 0, wm.ErrUnsupportedKey
}

// modifierMaskFor resolves a slice of modifier names to a packed Win32
// fsModifiers bitmask, including MOD_NOREPEAT so held keys don't re-fire.
func modifierMaskFor(mods []string) (uint32, error) {
	var mask uint32 = winModNoRepeat
	for _, m := range mods {
		bit, ok := modifierTable[strings.ToLower(strings.TrimSpace(m))]
		if !ok {
			return 0, wm.ErrUnsupportedModifier
		}
		mask |= bit
	}
	return mask, nil
}
