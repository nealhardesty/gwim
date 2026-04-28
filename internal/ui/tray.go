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
	eng     *engine.Engine
	version string

	// menu items kept around so we can update labels / checkmarks.
	mItemSuspend *systray.MenuItem
	mItemStatus  *systray.MenuItem
	mItemActive  *systray.MenuItem

	// shortcut menu items mapped by action ID for click dispatch.
	mu             sync.Mutex
	shortcutItems  map[string]*systray.MenuItem
	stopShortcutCh chan struct{}
}

// New constructs a Tray. The version string is shown in the menu footer.
func New(eng *engine.Engine, version string) *Tray {
	return &Tray{
		eng:            eng,
		version:        version,
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

	t.mItemSuspend = systray.AddMenuItem("Suspend GWiM", "Pause / resume hotkey dispatch")
	t.mItemActive = systray.AddMenuItem("Status: Active", "")
	t.mItemActive.Disable()
	t.mItemStatus = systray.AddMenuItem("Foreground: (unknown)", "")
	t.mItemStatus.Disable()

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
// the Suspend menu item.
func (t *Tray) handleSuspendToggle() {
	for {
		select {
		case <-t.stopShortcutCh:
			return
		case <-t.mItemSuspend.ClickedCh:
			cur := t.eng.Snapshot()
			t.eng.SetUserSuspended(!cur.UserSuspended)
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
				// Surface failures via the status item so the user
				// notices misbehaving apps without needing the log.
				t.mItemStatus.SetTitle(fmt.Sprintf("Error: %v", err))
			}
		}
	}
}

// refresh updates icon, status text, and toggle label to match state.
func (t *Tray) refresh(s engine.SuspensionState) {
	if s.Active() {
		systray.SetIcon(icon.Active())
		t.mItemSuspend.SetTitle("Suspend GWiM")
		t.mItemActive.SetTitle("Status: Active")
	} else {
		systray.SetIcon(icon.Suspended())
		switch {
		case s.UserSuspended:
			t.mItemSuspend.SetTitle("Activate GWiM")
			t.mItemActive.SetTitle("Status: Suspended (manual)")
		case s.AutoSuspended:
			t.mItemSuspend.SetTitle("Suspend GWiM")
			t.mItemActive.SetTitle("Status: Auto-suspended (blocklist)")
		default:
			t.mItemActive.SetTitle("Status: Suspended")
		}
	}

	if s.ActiveAppID == "" {
		t.mItemStatus.SetTitle("Foreground: (unknown)")
	} else if s.ActiveAppBlocked {
		t.mItemStatus.SetTitle(fmt.Sprintf("Foreground: %s [blocked]", s.ActiveAppID))
	} else {
		t.mItemStatus.SetTitle(fmt.Sprintf("Foreground: %s", s.ActiveAppID))
	}
}
