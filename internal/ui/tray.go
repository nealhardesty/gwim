// Package ui contains the menu-bar (macOS) / system-tray (Windows) UI.
//
// The UI is intentionally minimal:
//
//   - A title-bar icon that swaps between "active" and "suspended" variants.
//   - A "Suspend / Activate" toggle that drives Engine.SetUserSuspended.
//   - A "Shortcuts" submenu listing every action with its accelerator;
//     each item is clickable and triggers Engine.Execute, satisfying the
//     PRD §3.2 requirement that menu items act as remote-control buttons.
//   - A footer showing the foreground app and the auto-suspension reason.
//   - Quit.
package ui

import (
	"fmt"
	"sync"

	"github.com/getlantern/systray"

	"github.com/nealhardesty/gwim/internal/engine"
	"github.com/nealhardesty/gwim/internal/icon"
)

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

	// menu items kept around so we can update labels / checkmarks.
	mItemSuspend *systray.MenuItem
	mItemStatus  *systray.MenuItem
	mItemActive  *systray.MenuItem
	mItemAccess  *systray.MenuItem
	mItemLastErr *systray.MenuItem

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
	t.mItemLastErr = systray.AddMenuItem("Last action: ok", "")
	t.mItemLastErr.Disable()
	t.mItemLastErr.Hide()
	go t.handleAccessClick()

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

// refresh updates icon, status text, and toggle label to match state.
//
// Status text reflects which axis (user override vs. automatic blocklist)
// drove the current state, plus the foreground app, so the user can
// always tell why GWiM did or did not respond to a hotkey.
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
		switch s.UserMode {
		case engine.UserModeForceSuspended:
			t.mItemActive.SetTitle("Status: Suspended (forced off)")
		default: // UserModeAuto with AutoSuspended=true
			t.mItemActive.SetTitle("Status: Auto-suspended (blocklist)")
		}
	}

	switch {
	case s.ActiveAppID == "":
		t.mItemStatus.SetTitle("Foreground: (unknown)")
	case s.ActiveAppBlocked:
		t.mItemStatus.SetTitle(fmt.Sprintf("Foreground: %s [blocked]", s.ActiveAppID))
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
