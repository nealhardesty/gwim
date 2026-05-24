// Package wm defines the platform-agnostic interfaces for window management,
// hotkey registration, and active-application detection.
//
// Concrete implementations live under internal/platform/<os>. The engine and
// UI layers depend only on these interfaces, allowing GWiM to support
// additional operating systems without rewriting business logic.
package wm

import "errors"

// ScreenDirection enumerates the directions a window can be thrown between
// monitors on a multi-display setup.
type ScreenDirection string

const (
	ScreenWest  ScreenDirection = "west"
	ScreenEast  ScreenDirection = "east"
	ScreenNorth ScreenDirection = "north"
	ScreenSouth ScreenDirection = "south"
)

// Rect represents a rectangle in OS-native screen coordinates.
//
// All values are expressed as floats to preserve sub-pixel accuracy that
// macOS Accessibility APIs sometimes return. The coordinate origin is the
// top-left of the primary display, matching the convention used by the
// macOS Accessibility API and (with appropriate translation) Win32.
type Rect struct {
	X, Y, W, H float64
}

// Window abstracts a single OS-managed window the user can manipulate.
type Window interface {
	// GetFrame returns the current outer frame of the window.
	GetFrame() (Rect, error)
	// SetFrame moves and resizes the window to match the supplied rect.
	SetFrame(Rect) error
	// ToggleFullScreen toggles native full-screen mode (macOS Spaces / Win10 maximize variant).
	ToggleFullScreen() error
}

// WindowManager exposes window-discovery, screen-geometry, and active-app
// helpers. A single WindowManager instance is shared across the engine.
type WindowManager interface {
	// GetActiveWindow returns the currently focused window or an error if
	// no window is focused or accessibility access is denied.
	GetActiveWindow() (Window, error)

	// GetScreenFrame returns the visible frame (excluding menu bar / dock)
	// of the screen that contains the supplied window.
	GetScreenFrame(Window) (Rect, error)

	// GetActiveAppIdentifier returns the bundle identifier (macOS) or
	// executable name (Windows) of the foreground application.
	GetActiveAppIdentifier() (string, error)

	// MoveWindowToScreen translocates the supplied window to the adjacent
	// screen in the requested direction. Implementations must scale the
	// frame proportionally so the window keeps its relative size.
	MoveWindowToScreen(w Window, direction ScreenDirection) error
}

// WindowInfo describes a single OS-managed window for the Alt-Tab switcher.
//
// PID is the owning process; CGID is the macOS CGWindowID (or platform
// equivalent) that uniquely identifies the window for the lifetime of the
// process. Title and AppName are display-only.
//
// Minimized reports that the window is currently minimized (e.g. dropped
// to the macOS Dock). Hidden reports that the owning application is
// hidden (e.g. via Cmd+H / NSRunningApplication.hide). Either flag tells
// the overlay to dim the slot so users can still distinguish those
// windows; the platform's raise path un-minimises and un-hides on
// commit.
type WindowInfo struct {
	PID       int32
	CGID      uint32
	Title     string
	AppName   string
	Minimized bool
	Hidden    bool
}

// Switcher drives the Alt-Tab keyboard window switcher (per ALTTAB.md).
//
// The platform implementation owns the overlay UI, key event tap, MRU
// stash, and window-raising behaviour. The engine only needs to invoke
// the two open methods — they are bound to Option+Tab / Option+Shift+Tab.
//
// OpenForward and OpenBackward are also re-entrant: while the overlay is
// already open, calling them advances the highlight in the requested
// direction (the platform's native event tap normally handles this; the
// re-entrant behaviour exists so tray-menu clicks can drive the switcher).
type Switcher interface {
	OpenForward()
	OpenBackward()
}

// HotkeyHandler is invoked when a registered hotkey combination fires.
type HotkeyHandler func()

// HotkeyManager registers and dispatches global hotkeys.
//
// SetSuspended toggles whether registered hotkeys are active. While
// suspended (manual suspend from the menu or toggle hotkey), key
// combinations are passed through to the foreground application unhandled,
// e.g. so shortcuts can reach a remote-desktop client unchanged.
type HotkeyManager interface {
	// Register adds a new global hotkey. Modifiers use canonical names:
	// "ctrl", "alt" (or "option"), "shift", "cmd" (or "win").
	// Key names follow the same convention used in shortcuts.go (e.g.
	// "h", "1", "left", "return").
	//
	// Hotkeys registered via Register are SUSPENDABLE: SetSuspended(true)
	// physically unregisters them with the OS so the keystroke flows to
	// the foreground app.
	Register(modifiers []string, key string, handler HotkeyHandler) error

	// RegisterPersistent registers a hotkey that survives suspension.
	//
	// Persistent hotkeys are NEVER unregistered by SetSuspended, so they
	// continue to fire while regular hotkeys are suspended. Reserved for
	// control-channel commands like the "toggle GWiM on/off" hotkey, where
	// the user must always be able to drive GWiM regardless of suspension state.
	RegisterPersistent(modifiers []string, key string, handler HotkeyHandler) error

	// SetSuspended enables (false) or disables (true) hotkey dispatch
	// for hotkeys registered via Register. Persistent hotkeys are
	// unaffected. When suspended, implementations should ensure the OS
	// does not consume the keystroke so it can reach the foreground app.
	SetSuspended(bool)

	// Suspended reports the current suspension state.
	Suspended() bool

	// Start begins listening for hotkey events. Must be called from the
	// thread that owns the platform event loop on platforms that require
	// it (macOS in particular).
	Start() error

	// Stop releases all registered hotkeys and stops the event loop.
	Stop()
}

// Common sentinel errors returned by implementations.
var (
	// ErrNoActiveWindow indicates no focused window could be located.
	ErrNoActiveWindow = errors.New("wm: no active window")

	// ErrAccessibilityDenied indicates the OS denied the accessibility
	// permission required to manipulate windows.
	ErrAccessibilityDenied = errors.New("wm: accessibility permission denied")

	// ErrUnsupportedKey indicates the supplied key name has no platform
	// keycode mapping.
	ErrUnsupportedKey = errors.New("wm: unsupported key")

	// ErrUnsupportedModifier indicates the supplied modifier name has no
	// platform mapping.
	ErrUnsupportedModifier = errors.New("wm: unsupported modifier")
)
