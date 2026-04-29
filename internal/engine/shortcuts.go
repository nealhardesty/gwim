package engine

import "github.com/nealhardesty/gwim/internal/wm"

// Shortcut binds an Action to a global hotkey combination.
//
// One Action may have multiple Shortcuts (e.g. "Snap Left Half" is
// triggered by both Ctrl+Alt+Left and Ctrl+Alt+H per PRD §3.3).
type Shortcut struct {
	// ActionID matches Action.ID in the registered action list.
	ActionID string

	// Modifiers uses canonical names accepted by HotkeyManager.Register
	// (see internal/platform/macos/keycodes.go).
	Modifiers []string

	// Key is the physical key name (also see keycodes.go).
	Key string
}

// ToggleHotkey is the persistent hotkey bound to ToggleUserSuspended.
//
// "Persistent" means the OS keeps it registered even while regular
// shortcuts are unregistered (manual suspend), so the user can always
// reclaim or release control of GWiM from the keyboard.
type ToggleHotkey struct {
	Modifiers []string
	Key       string
}

// Format renders the toggle accelerator using macOS glyph shorthand.
func (h *ToggleHotkey) Format() string {
	if h == nil {
		return ""
	}
	return formatShortcut(Shortcut{Modifiers: h.Modifiers, Key: h.Key})
}

// DefaultToggleHotkey returns the default Ctrl+Alt+X mapping.
func DefaultToggleHotkey() *ToggleHotkey {
	return &ToggleHotkey{
		Modifiers: []string{"ctrl", "alt"},
		Key:       "x",
	}
}

// Modifier groups for readability. The hotkey scheme below mirrors the
// legacy Hammerspoon configuration described in DESIGN.md §3.
var (
	tilingMods   = []string{"ctrl", "alt"}
	moveMods     = []string{"ctrl", "alt", "shift"}
	resizeMods   = []string{"ctrl", "alt", "shift", "cmd"}
	throwingMods = []string{"ctrl", "alt", "cmd"}
)

// DefaultActions returns every Action GWiM ships with, in display order.
//
// The same slice powers both the global hotkey registration loop AND the
// "Shortcuts" submenu in the tray, ensuring the two stay in lockstep.
func DefaultActions() []Action {
	return []Action{
		// PRD §3.3 - Halves
		snapAction("snap.left-half", "Snap Left Half", "Halves", snapLeftHalf),
		snapAction("snap.right-half", "Snap Right Half", "Halves", snapRightHalf),
		snapAction("snap.top-half", "Snap Top Half", "Halves", snapTopHalf),
		snapAction("snap.bottom-half", "Snap Bottom Half", "Halves", snapBottomHalf),

		// PRD §3.3 - Quarters
		snapAction("snap.top-left", "Snap Top-Left Quarter", "Quarters", snapTopLeftQuarter),
		snapAction("snap.top-right", "Snap Top-Right Quarter", "Quarters", snapTopRightQuarter),
		snapAction("snap.bottom-left", "Snap Bottom-Left Quarter", "Quarters", snapBottomLeftQuarter),
		snapAction("snap.bottom-right", "Snap Bottom-Right Quarter", "Quarters", snapBottomRightQuarter),

		// PRD §3.3 - Thirds
		snapAction("snap.left-third", "Snap Left Third", "Thirds", snapLeftThird),
		snapAction("snap.middle-third", "Snap Middle Third", "Thirds", snapMiddleThird),
		snapAction("snap.right-third", "Snap Right Third", "Thirds", snapRightThird),

		// PRD §3.3 - Fourths
		fourthAction("snap.fourth-1", "Snap Fourth 1 (Leftmost)", 0),
		fourthAction("snap.fourth-2", "Snap Fourth 2", 1),
		fourthAction("snap.fourth-3", "Snap Fourth 3", 2),
		fourthAction("snap.fourth-4", "Snap Fourth 4 (Rightmost)", 3),

		// PRD §3.3 - Bottom horizontal strips
		snapAction("snap.bottom-strip-full", "Bottom Strip (Full Width)", "Strips", snapBottomStripFull),
		snapAction("snap.bottom-strip-left", "Bottom Strip (Left Half)", "Strips", snapBottomStripLeft),
		snapAction("snap.bottom-strip-right", "Bottom Strip (Right Half)", "Strips", snapBottomStripRight),

		// PRD §3.3 - Maximize
		snapAction("snap.maximize", "Maximize (Frame Only)", "Maximize", snapMaximize),
		fullscreenAction(),

		// PRD §3.4 - Relative move (Ctrl+Alt+Shift)
		moveAction("move.left", "Move Left 100px", -moveStep, 0),
		moveAction("move.right", "Move Right 100px", moveStep, 0),
		moveAction("move.up", "Move Up 100px", 0, -moveStep),
		moveAction("move.down", "Move Down 100px", 0, moveStep),

		// PRD §3.5 - Relative resize (Ctrl+Alt+Shift+Cmd)
		resizeAction("resize.width-dec", "Decrease Width 100px", -moveStep, 0),
		resizeAction("resize.width-inc", "Increase Width 100px", moveStep, 0),
		resizeAction("resize.height-dec", "Decrease Height 100px", 0, -moveStep),
		resizeAction("resize.height-inc", "Increase Height 100px", 0, moveStep),

		// PRD §3.6 - Screen jumping (Ctrl+Alt+Cmd)
		throwAction("throw.west", "Throw to West Screen", wm.ScreenWest),
		throwAction("throw.east", "Throw to East Screen", wm.ScreenEast),
		throwAction("throw.north", "Throw to North Screen", wm.ScreenNorth),
		throwAction("throw.south", "Throw to South Screen", wm.ScreenSouth),
	}
}

// DefaultShortcuts returns every (modifiers, key) -> ActionID binding GWiM
// ships with, transcribed verbatim from the DESIGN.md hotkey table.
//
// Multiple shortcuts per action are allowed (e.g. arrow keys + h/j/k/l)
// and result in multiple OS-level hotkey registrations sharing one Run.
func DefaultShortcuts() []Shortcut {
	return []Shortcut{
		// PRD §3.3 Halves
		{ActionID: "snap.left-half", Modifiers: tilingMods, Key: "left"},
		{ActionID: "snap.left-half", Modifiers: tilingMods, Key: "h"},
		{ActionID: "snap.right-half", Modifiers: tilingMods, Key: "right"},
		{ActionID: "snap.right-half", Modifiers: tilingMods, Key: "l"},
		{ActionID: "snap.top-half", Modifiers: tilingMods, Key: "up"},
		{ActionID: "snap.bottom-half", Modifiers: tilingMods, Key: "down"},

		// PRD §3.3 Quarters
		{ActionID: "snap.top-left", Modifiers: tilingMods, Key: "u"},
		{ActionID: "snap.top-right", Modifiers: tilingMods, Key: "i"},
		{ActionID: "snap.bottom-left", Modifiers: tilingMods, Key: "j"},
		{ActionID: "snap.bottom-right", Modifiers: tilingMods, Key: "k"},

		// PRD §3.3 Thirds
		{ActionID: "snap.left-third", Modifiers: tilingMods, Key: "1"},
		{ActionID: "snap.middle-third", Modifiers: tilingMods, Key: "2"},
		{ActionID: "snap.right-third", Modifiers: tilingMods, Key: "3"},

		// PRD §3.3 Fourths
		{ActionID: "snap.fourth-1", Modifiers: tilingMods, Key: "4"},
		{ActionID: "snap.fourth-2", Modifiers: tilingMods, Key: "5"},
		{ActionID: "snap.fourth-3", Modifiers: tilingMods, Key: "6"},
		{ActionID: "snap.fourth-4", Modifiers: tilingMods, Key: "7"},

		// PRD §3.3 Bottom strips
		{ActionID: "snap.bottom-strip-full", Modifiers: tilingMods, Key: "m"},
		{ActionID: "snap.bottom-strip-left", Modifiers: tilingMods, Key: "n"},
		{ActionID: "snap.bottom-strip-right", Modifiers: tilingMods, Key: ","},

		// PRD §3.3 Maximize / fullscreen
		{ActionID: "snap.maximize", Modifiers: tilingMods, Key: "return"},
		{ActionID: "fullscreen.toggle", Modifiers: tilingMods, Key: "f"},

		// PRD §3.4 Relative move (note: design says j=up, k=down)
		{ActionID: "move.left", Modifiers: moveMods, Key: "h"},
		{ActionID: "move.left", Modifiers: moveMods, Key: "left"},
		{ActionID: "move.right", Modifiers: moveMods, Key: "l"},
		{ActionID: "move.right", Modifiers: moveMods, Key: "right"},
		{ActionID: "move.up", Modifiers: moveMods, Key: "j"},
		{ActionID: "move.up", Modifiers: moveMods, Key: "up"},
		{ActionID: "move.down", Modifiers: moveMods, Key: "k"},
		{ActionID: "move.down", Modifiers: moveMods, Key: "down"},

		// PRD §3.5 Relative resize
		{ActionID: "resize.width-dec", Modifiers: resizeMods, Key: "h"},
		{ActionID: "resize.width-dec", Modifiers: resizeMods, Key: "left"},
		{ActionID: "resize.width-inc", Modifiers: resizeMods, Key: "l"},
		{ActionID: "resize.width-inc", Modifiers: resizeMods, Key: "right"},
		{ActionID: "resize.height-dec", Modifiers: resizeMods, Key: "j"},
		{ActionID: "resize.height-dec", Modifiers: resizeMods, Key: "up"},
		{ActionID: "resize.height-inc", Modifiers: resizeMods, Key: "k"},
		{ActionID: "resize.height-inc", Modifiers: resizeMods, Key: "down"},

		// PRD §3.6 Screen throwing
		{ActionID: "throw.west", Modifiers: throwingMods, Key: "left"},
		{ActionID: "throw.west", Modifiers: throwingMods, Key: "h"},
		{ActionID: "throw.east", Modifiers: throwingMods, Key: "right"},
		{ActionID: "throw.east", Modifiers: throwingMods, Key: "l"},
		{ActionID: "throw.north", Modifiers: throwingMods, Key: "up"},
		{ActionID: "throw.north", Modifiers: throwingMods, Key: "k"},
		{ActionID: "throw.south", Modifiers: throwingMods, Key: "down"},
		{ActionID: "throw.south", Modifiers: throwingMods, Key: "j"},
	}
}

// PrimaryShortcutFor returns the first shortcut bound to the given action,
// formatted as a human-readable accelerator string (e.g. "⌃⌥H").
// Returns "" when no shortcut is bound.
func PrimaryShortcutFor(actionID string, shortcuts []Shortcut) string {
	for _, s := range shortcuts {
		if s.ActionID == actionID {
			return formatShortcut(s)
		}
	}
	return ""
}

// formatShortcut renders a Shortcut using the standard macOS glyph
// shorthand (⌃ ⌥ ⇧ ⌘) so the tray menu reads naturally.
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

// keyDisplay returns the human-friendly label for a key name.
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
		// Letter / digit / punctuation – upper-case for readability.
		c := k[0]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		return string(c)
	}
	return k
}
