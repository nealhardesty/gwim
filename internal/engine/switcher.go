package engine

import (
	"errors"

	"github.com/nealhardesty/gwim/internal/wm"
)

// Action category surfaced by the tray for the Alt-Tab switcher.
const switcherCategory = "Window Switcher"

// SwitcherActions builds the engine actions that drive the Alt-Tab
// window switcher (per ALTTAB.md). They forward to the supplied wm.Switcher.
//
// Returned actions are intended to be appended to engine.DefaultActions
// before constructing the engine. Pair with SwitcherShortcuts to bind
// Option+Tab and Option+Shift+Tab.
//
// The wm.WindowManager argument to Action.Run is unused — the switcher
// owns its own window enumeration / raising path.
func SwitcherActions(s wm.Switcher) []Action {
	if s == nil {
		return nil
	}
	return []Action{
		{
			ID:       "switcher.next",
			Label:    "Switch Window (Forward)",
			Category: switcherCategory,
			Run: func(_ wm.WindowManager) error {
				if s == nil {
					return errors.New("switcher: not initialised")
				}
				s.OpenForward()
				return nil
			},
		},
		{
			ID:       "switcher.prev",
			Label:    "Switch Window (Backward)",
			Category: switcherCategory,
			Run: func(_ wm.WindowManager) error {
				if s == nil {
					return errors.New("switcher: not initialised")
				}
				s.OpenBackward()
				return nil
			},
		},
	}
}

// SwitcherShortcuts returns the default keyboard bindings for the
// Alt-Tab switcher: Option+Tab forward, Option+Shift+Tab backward
// (per ALTTAB.md §4.1.1).
func SwitcherShortcuts() []Shortcut {
	return []Shortcut{
		{ActionID: "switcher.next", Modifiers: []string{"alt"}, Key: "tab"},
		{ActionID: "switcher.prev", Modifiers: []string{"alt", "shift"}, Key: "tab"},
	}
}
