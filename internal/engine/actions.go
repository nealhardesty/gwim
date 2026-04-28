package engine

import (
	"fmt"

	"github.com/nealhardesty/gwim/internal/wm"
)

// Action is the canonical unit of work in GWiM.
//
// Every action is exposed two ways:
//   - As a global hotkey (registered via wm.HotkeyManager).
//   - As a clickable item in the menu-bar shortcut reference.
//
// The same Run function is used for both invocation paths, satisfying the
// design's DRY requirement (PRD §4.5).
type Action struct {
	// ID is a stable identifier used for testing, logging and tray menu
	// item lookup. Format: "category.name" (e.g. "snap.left-half").
	ID string

	// Label is the human-readable name displayed in the tray menu.
	Label string

	// Category groups actions in the tray's "Shortcuts" submenu.
	Category string

	// Run performs the action. It is invoked on a worker goroutine and
	// MUST be safe to call concurrently with other actions (the wm
	// implementations handle this).
	Run func(wm.WindowManager) error
}

// withActiveWindow is a convenience wrapper for actions that need to read
// the active window's frame, the screen frame, then write a new frame.
//
// It centralises the (very common) error-handling chain so individual
// action definitions stay declarative.
func withActiveWindow(w wm.WindowManager, fn func(win wm.Window, screen wm.Rect) error) error {
	win, err := w.GetActiveWindow()
	if err != nil {
		return fmt.Errorf("get active window: %w", err)
	}
	screen, err := w.GetScreenFrame(win)
	if err != nil {
		return fmt.Errorf("get screen frame: %w", err)
	}
	return fn(win, screen)
}

// snapAction returns an Action that resets the active window to the rect
// produced by `compute(screen)`.
func snapAction(id, label, category string, compute func(wm.Rect) wm.Rect) Action {
	return Action{
		ID: id, Label: label, Category: category,
		Run: func(w wm.WindowManager) error {
			return withActiveWindow(w, func(win wm.Window, screen wm.Rect) error {
				return win.SetFrame(compute(screen))
			})
		},
	}
}

// fourthAction returns an Action that snaps the active window to the Nth
// vertical fourth of the screen.
func fourthAction(id, label string, index int) Action {
	return Action{
		ID: id, Label: label, Category: "Fourths",
		Run: func(w wm.WindowManager) error {
			return withActiveWindow(w, func(win wm.Window, screen wm.Rect) error {
				return win.SetFrame(snapFourth(screen, index))
			})
		},
	}
}

// moveAction returns an Action that translates the active window by (dx, dy).
func moveAction(id, label string, dx, dy float64) Action {
	return Action{
		ID: id, Label: label, Category: "Move",
		Run: func(w wm.WindowManager) error {
			win, err := w.GetActiveWindow()
			if err != nil {
				return fmt.Errorf("get active window: %w", err)
			}
			frame, err := win.GetFrame()
			if err != nil {
				return fmt.Errorf("get frame: %w", err)
			}
			return win.SetFrame(moveBy(frame, dx, dy))
		},
	}
}

// resizeAction returns an Action that resizes the active window by (dw, dh).
func resizeAction(id, label string, dw, dh float64) Action {
	return Action{
		ID: id, Label: label, Category: "Resize",
		Run: func(w wm.WindowManager) error {
			win, err := w.GetActiveWindow()
			if err != nil {
				return fmt.Errorf("get active window: %w", err)
			}
			frame, err := win.GetFrame()
			if err != nil {
				return fmt.Errorf("get frame: %w", err)
			}
			return win.SetFrame(resizeBy(frame, dw, dh))
		},
	}
}

// throwAction returns an Action that moves the active window to the
// adjacent monitor in the given direction.
func throwAction(id, label string, dir wm.ScreenDirection) Action {
	return Action{
		ID: id, Label: label, Category: "Screen",
		Run: func(w wm.WindowManager) error {
			win, err := w.GetActiveWindow()
			if err != nil {
				return fmt.Errorf("get active window: %w", err)
			}
			return w.MoveWindowToScreen(win, dir)
		},
	}
}

// fullscreenAction toggles native macOS fullscreen on the active window.
func fullscreenAction() Action {
	return Action{
		ID: "fullscreen.toggle", Label: "Toggle Native Fullscreen", Category: "Maximize",
		Run: func(w wm.WindowManager) error {
			win, err := w.GetActiveWindow()
			if err != nil {
				return fmt.Errorf("get active window: %w", err)
			}
			return win.ToggleFullScreen()
		},
	}
}
