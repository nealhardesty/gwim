// Package ui contains the menu-bar (macOS) / system-tray (Windows) UI.
//
// The UI is intentionally minimal:
//
//   - A title-bar icon that swaps between "active" and "suspended" variants.
//   - A "Suspend / Activate" toggle that drives Engine.SetUserSuspended.
//   - A "Shortcuts" submenu listing every action with its accelerator;
//     each item is clickable and triggers Engine.Execute, satisfying the
//     PRD §3.2 requirement that menu items act as remote-control buttons.
//   - A footer showing the foreground app and permission status rows.
//   - Optional Open at Login (platform supplies hooks on macOS).
//   - Quit.
package ui

import (
	"fmt"
	"log"
	"sync"

	"github.com/getlantern/systray"

	"github.com/nealhardesty/gwim/internal/engine"
	"github.com/nealhardesty/gwim/internal/icon"
)

// LaunchAtLoginHooks lets a platform package drive the Open at Login menu
// item. If LaunchAtLogin is nil or Supported returns false, no item is added.
type LaunchAtLoginHooks struct {
	Supported func() bool
	IsOn      func() bool
	Set       func(enable bool) error
}

// Tray is the menu-bar controller. It owns the systray menu items and
// reacts to engine state changes.
type Tray struct {
	eng         *engine.Engine
	version     string
	toggleAccel string // pre-formatted glyph string (e.g. "⌃⌥X"), "" if none

	// OpenAccessibilitySettings, if set, is invoked when the user clicks
	// the AX status item. Optional — main packages on macOS supply the
	// `open x-apple.systempreferences:...` shell shortcut.
	OpenAccessibilitySettings func()

	// OpenScreenRecordingSettings mirrors OpenAccessibilitySettings for
	// the Screen Recording permission used by the Alt-Tab switcher's
	// live thumbnails. May be nil; if set, the tray surfaces a clickable
	// "Screen Recording: …" row that triggers it.
	OpenScreenRecordingSettings func()

	// RequestScreenRecording, when non-nil, is invoked the first time
	// the user clicks the Screen Recording row to add GWiM to System
	// Settings → Privacy & Security → Screen Recording. macOS prompts
	// once per app; subsequent clicks should fall through to
	// OpenScreenRecordingSettings to nudge the user.
	RequestScreenRecording func() bool

	// LaunchAtLogin, if set and Supported() is true, adds a checkable
	// "Open at Login" row (macOS 13+ from GWiM.app).
	LaunchAtLogin *LaunchAtLoginHooks

	// menu items kept around so we can update labels / checkmarks.
	mItemSuspend *systray.MenuItem
	mItemStatus  *systray.MenuItem
	mItemActive  *systray.MenuItem
	mItemAccess  *systray.MenuItem
	mItemScreen  *systray.MenuItem
	mItemLastErr *systray.MenuItem
	mItemLaunch  *systray.MenuItem

	// shortcut menu items mapped by action ID for click dispatch.
	mu             sync.Mutex
	shortcutItems  map[string]*systray.MenuItem
	stopShortcutCh chan struct{}
}

// New constructs a Tray. The version string is shown in the menu footer;
// toggleAccel, if non-empty, is appended to the Suspend/Activate label
// so the user can see the keyboard shortcut.
func New(eng *engine.Engine, version, toggleAccel string) *Tray {
	return &Tray{
		eng:            eng,
		version:        version,
		toggleAccel:    toggleAccel,
		shortcutItems:  make(map[string]*systray.MenuItem),
		stopShortcutCh: make(chan struct{}),
	}
}

// Run hands control to systray; it must be called from the main goroutine
// on macOS because the underlying NSApplication run loop demands it.
//
// onReady is invoked from systray's setup callback after the menu is built;
// onExit fires when the user picks "Quit".
func (t *Tray) Run(onReady, onExit func()) {
	systray.Run(func() {
		t.build()
		if onReady != nil {
			onReady()
		}
	}, func() {
		close(t.stopShortcutCh)
		if onExit != nil {
			onExit()
		}
	})
}

// build populates the systray menu. Called once on the systray goroutine.
func (t *Tray) build() {
	systray.SetIcon(icon.Active())
	systray.SetTitle("") // icon-only; title takes precious menu-bar real estate
	systray.SetTooltip(fmt.Sprintf("GWiM %s — Keyboard window manager", t.version))

	t.mItemSuspend = systray.AddMenuItem(t.suspendLabel(true), "Pause / resume hotkey dispatch")
	t.mItemActive = systray.AddMenuItem("Status: Active", "")
	t.mItemActive.Disable()
	t.mItemStatus = systray.AddMenuItem("Foreground: (unknown)", "")
	t.mItemStatus.Disable()
	// AX status is clickable — clicking opens System Settings so the
	// user can fix a denied permission without hunting through menus.
	t.mItemAccess = systray.AddMenuItem("Accessibility: (checking…)", "Click to open System Settings")
	// Screen Recording is the optional companion permission — granted
	// state enables live window thumbnails in the Alt-Tab switcher.
	t.mItemScreen = systray.AddMenuItem("Screen Recording: (checking…)", "Click to enable live thumbnails in the switcher")
	t.mItemScreen.Hide() // unhide once we know the state
	t.mItemLastErr = systray.AddMenuItem("Last action: ok", "")
	t.mItemLastErr.Disable()
	t.mItemLastErr.Hide()
	go t.handleAccessClick()
	go t.handleScreenClick()

	if t.LaunchAtLogin != nil && t.LaunchAtLogin.Supported != nil && t.LaunchAtLogin.Supported() &&
		t.LaunchAtLogin.IsOn != nil && t.LaunchAtLogin.Set != nil {
		on := t.LaunchAtLogin.IsOn()
		t.mItemLaunch = systray.AddMenuItemCheckbox("Open at Login",
			"Launch GWiM when you log in (macOS 13+, from GWiM.app)", on)
		go t.handleLaunchAtLogin()
	}

	systray.AddSeparator()

	shortcutsRoot := systray.AddMenuItem("Shortcuts", "Click any shortcut to run it on the active window")
	t.buildShortcutsMenu(shortcutsRoot)

	systray.AddSeparator()

	versionItem := systray.AddMenuItem("GWiM "+t.version, "")
	versionItem.Disable()

	mQuit := systray.AddMenuItem("Quit", "Exit GWiM")

	go t.handleSuspendToggle()
	go t.handleQuit(mQuit)

	t.eng.AddListener(func(s engine.SuspensionState) {
		t.refresh(s)
	})
	t.refresh(t.eng.Snapshot())
}

// buildShortcutsMenu groups actions by category in a submenu, attaching
// each item's primary accelerator to its label.
func (t *Tray) buildShortcutsMenu(root *systray.MenuItem) {
	actions := t.eng.Actions()
	shortcuts := t.eng.Shortcuts()

	// Group by category preserving first-seen order.
	type entry struct {
		category string
		items    []engine.Action
	}
	var groups []*entry
	idx := map[string]*entry{}
	for _, a := range actions {
		g, ok := idx[a.Category]
		if !ok {
			g = &entry{category: a.Category}
			idx[a.Category] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, a)
	}

	for _, g := range groups {
		sub := root.AddSubMenuItem(g.category, "")
		for _, a := range g.items {
			a := a
			label := a.Label
			if accel := engine.PrimaryShortcutFor(a.ID, shortcuts); accel != "" {
				label = fmt.Sprintf("%s   %s", label, accel)
			}
			item := sub.AddSubMenuItem(label, fmt.Sprintf("Run %s on the active window", a.Label))
			t.mu.Lock()
			t.shortcutItems[a.ID] = item
			t.mu.Unlock()
			go t.handleShortcutClick(a.ID, item)
		}
	}
}

// handleSuspendToggle runs in its own goroutine and reacts to clicks on
// the Suspend menu item. Uses ToggleUserSuspended so the click overrides
// auto-suspension exactly like the Ctrl+Alt+X hotkey does.
func (t *Tray) handleSuspendToggle() {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-t.mItemSuspend.ClickedCh:
			t.eng.ToggleUserSuspended()
		}
	}
}

// handleQuit waits for a Quit click and tears down systray.
func (t *Tray) handleQuit(item *systray.MenuItem) {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-item.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// handleShortcutClick wires a single shortcut menu item to the engine.
// Per PRD §3.2 these must run regardless of suspension state — a manual
// click is an explicit user request.
func (t *Tray) handleShortcutClick(actionID string, item *systray.MenuItem) {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-item.ClickedCh:
			if err := t.eng.Execute(actionID); err != nil {
				// Surface failures via the dedicated last-error row.
				t.mItemLastErr.SetTitle(fmt.Sprintf("Last action: %v", err))
				t.mItemLastErr.Show()
			}
		}
	}
}

// handleAccessClick handles clicks on the Accessibility status item.
//
// Each click does two things:
//  1. Re-runs the engine's AX check so the menu reflects truth even if
//     the user just toggled the permission in System Settings.
//  2. If still denied (or first click), invokes the platform-supplied
//     OpenAccessibilitySettings hook so the user can fix it in one step.
func (t *Tray) handleAccessClick() {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-t.mItemAccess.ClickedCh:
			t.eng.RefreshAccessibility()
			if t.OpenAccessibilitySettings != nil {
				t.OpenAccessibilitySettings()
			}
		}
	}
}

// handleScreenClick handles clicks on the Screen Recording status item.
//
// First click triggers RequestScreenRecording (which prompts macOS to
// add GWiM to System Settings → Privacy & Security → Screen Recording).
// Subsequent clicks open the panel directly, since on macOS 14+ the
// permission only takes effect after relaunch.
func (t *Tray) handleScreenClick() {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-t.mItemScreen.ClickedCh:
			if t.RequestScreenRecording != nil {
				t.RequestScreenRecording()
			}
			t.eng.RefreshScreenRecording()
			if t.OpenScreenRecordingSettings != nil {
				t.OpenScreenRecordingSettings()
			}
		}
	}
}

func (t *Tray) handleLaunchAtLogin() {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-t.mItemLaunch.ClickedCh:
			if t.LaunchAtLogin == nil || t.LaunchAtLogin.IsOn == nil || t.LaunchAtLogin.Set == nil {
				continue
			}
			next := !t.LaunchAtLogin.IsOn()
			if err := t.LaunchAtLogin.Set(next); err != nil {
				log.Printf("open at login: %v", err)
				t.mItemLastErr.SetTitle(fmt.Sprintf("Open at login: %v", err))
				t.mItemLastErr.Show()
				if t.LaunchAtLogin.IsOn() {
					t.mItemLaunch.Check()
				} else {
					t.mItemLaunch.Uncheck()
				}
				continue
			}
			if next {
				t.mItemLaunch.Check()
			} else {
				t.mItemLaunch.Uncheck()
			}
		}
	}
}

// refresh updates icon, status text, and toggle label to match state.
//
// Status text reflects manual suspend vs active (including after-toggle
// “forced on”), plus the foreground app for context.
func (t *Tray) refresh(s engine.SuspensionState) {
	if s.Active() {
		systray.SetIcon(icon.Active())
		t.mItemSuspend.SetTitle(t.suspendLabel(true))
		switch s.UserMode {
		case engine.UserModeForceActive:
			t.mItemActive.SetTitle("Status: Active (forced on)")
		default:
			t.mItemActive.SetTitle("Status: Active")
		}
	} else {
		systray.SetIcon(icon.Suspended())
		t.mItemSuspend.SetTitle(t.suspendLabel(false))
		t.mItemActive.SetTitle("Status: Suspended")
	}

	switch {
	case s.ActiveAppID == "":
		t.mItemStatus.SetTitle("Foreground: (unknown)")
	default:
		t.mItemStatus.SetTitle(fmt.Sprintf("Foreground: %s", s.ActiveAppID))
	}

	// Accessibility row — the headline diagnostic. Without AX permission
	// every action silently no-ops; the user's only clue used to be log
	// output they couldn't see. This row makes it obvious.
	switch {
	case !s.AccessibilityChecked:
		t.mItemAccess.SetTitle("Accessibility: (unknown)")
	case s.AccessibilityGranted:
		t.mItemAccess.SetTitle("Accessibility: granted ✓")
	default:
		t.mItemAccess.SetTitle("Accessibility: DENIED — click to fix")
	}

	// Screen Recording row — informational. The Alt-Tab switcher
	// degrades gracefully (app icons only) when this is denied, so this
	// is a "click to upgrade to live thumbnails" affordance rather than
	// a hard error. We hide the row entirely when the engine wasn't
	// configured with a probe (e.g. unit tests).
	if !s.ScreenRecordingChecked {
		t.mItemScreen.Hide()
	} else {
		t.mItemScreen.Show()
		if s.ScreenRecordingGranted {
			t.mItemScreen.SetTitle("Screen Recording: granted ✓")
		} else {
			t.mItemScreen.SetTitle("Screen Recording: off — click to enable thumbnails")
		}
	}

	if s.LastActionError != "" {
		t.mItemLastErr.SetTitle("Last action: " + s.LastActionError)
		t.mItemLastErr.Show()
	} else {
		t.mItemLastErr.Hide()
	}
}

// suspendLabel renders the Suspend/Activate menu label, optionally
// appending the toggle accelerator (e.g. "Suspend GWiM   ⌃⌥X").
func (t *Tray) suspendLabel(active bool) string {
	base := "Suspend GWiM"
	if !active {
		base = "Activate GWiM"
	}
	if t.toggleAccel == "" {
		return base
	}
	return fmt.Sprintf("%s   %s", base, t.toggleAccel)
}
