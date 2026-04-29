package engine

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nealhardesty/gwim/internal/wm"
)

// UserMode is the user's explicit suspension preference.
//
//   - UserModeAuto — default at startup: hotkeys follow effective activity
//     (always active unless changed by toggle; no automatic suspension).
//   - UserModeForceActive — GWiM stays on (after user toggles resume).
//   - UserModeForceSuspended — GWiM hotkeys are off until toggled back on.
//
// The tray toggle and Ctrl+Alt+X flip this mode based on current effective
// state so the user can suspend or resume without quitting.
type UserMode int32

const (
	UserModeAuto UserMode = iota
	UserModeForceActive
	UserModeForceSuspended
)

// String renders the mode for status menus and logs.
func (m UserMode) String() string {
	switch m {
	case UserModeForceActive:
		return "ForceActive"
	case UserModeForceSuspended:
		return "ForceSuspended"
	default:
		return "Auto"
	}
}

// Engine is the heart of GWiM: it holds the action table, shortcut
// bindings, and user-mode override for manual suspension.
//
// It deliberately exposes a tiny surface (Run / Execute /
// ToggleUserSuspended / SetUserMode / Stop / Snapshot) so the UI and main
// packages stay decoupled.
type Engine struct {
	wmgr      wm.WindowManager
	hkmgr     wm.HotkeyManager
	actions   []Action
	shortcuts []Shortcut
	toggle    *ToggleHotkey

	mu      sync.RWMutex
	actByID map[string]*Action

	userMode atomic.Int32 // stores UserMode

	axCheck   func() bool
	axGranted atomic.Bool // last result of axCheck

	srCheck   func() bool
	srGranted atomic.Bool // last result of srCheck

	lastErrMu sync.Mutex
	lastErr   string // last action failure, surfaced via Snapshot

	// Listeners notified whenever combined suspension state changes,
	// allowing the tray UI to refresh its checkmarks without polling.
	listenerMu sync.Mutex
	listeners  []func(state SuspensionState)

	pollInterval time.Duration
	logger       *log.Logger
}

// SuspensionState is a snapshot of the engine's effective suspension state
// plus enough context for the tray UI to render its labels.
//
// UserSuspended is a compat alias for UserMode == UserModeForceSuspended;
// UserMode is the canonical truth and Active() reflects effective dispatch.
//
// AccessibilityGranted is platform-supplied (via Config.AccessibilityCheck)
// and exposed here so the tray can warn the user when permission has been
// silently revoked — a notorious macOS TCC failure mode triggered by
// rebuilding the binary with a different code signature.
//
// ScreenRecordingGranted mirrors AccessibilityGranted but for the Screen
// Recording entitlement (used by the Alt-Tab switcher's live thumbnails).
// Switcher functionality degrades gracefully when denied, so this is an
// informational signal for the tray rather than a hard gate.
type SuspensionState struct {
	UserMode               UserMode
	UserSuspended          bool // == UserMode == UserModeForceSuspended (compat alias)
	ActiveAppID            string
	ManagedHotkeyCount     int
	AccessibilityChecked   bool   // false when no Config.AccessibilityCheck supplied
	AccessibilityGranted   bool   // last result of AccessibilityCheck()
	ScreenRecordingChecked bool   // false when no Config.ScreenRecordingCheck supplied
	ScreenRecordingGranted bool   // last result of ScreenRecordingCheck()
	LastActionError        string // most recent action failure message, "" if clean
}

// Active reports whether GWiM is currently dispatching hotkeys.
//
// Explicit force modes win; UserModeAuto means active unless the user has
// chosen suspension via toggle (which moves the mode out of Auto).
func (s SuspensionState) Active() bool {
	switch s.UserMode {
	case UserModeForceActive:
		return true
	case UserModeForceSuspended:
		return false
	default:
		return true
	}
}

// Config bundles the dependencies and tunables for a new Engine.
//
// PollInterval defaults to 500ms when zero — used for periodic
// accessibility and screen-recording permission probes so the tray stays
// accurate if the user changes System Settings.
//
// ToggleHotkey, when non-nil, registers a persistent hotkey that flips
// the user-mode override. Persistent means it remains live even while
// regular shortcuts are suspended (manual suspend) so the user can always
// resume or suspend from the keyboard.
//
// AccessibilityCheck, when non-nil, is invoked on every poller tick and
// after every failed action to refresh SuspensionState.AccessibilityGranted.
// It MUST be a NON-prompting check (returning the current grant state
// without showing a dialog) — repeated prompts would be hostile.
//
// ScreenRecordingCheck mirrors AccessibilityCheck for the Screen Recording
// permission. Same prompting contract: NON-prompting only.
type Config struct {
	WindowManager        wm.WindowManager
	HotkeyManager        wm.HotkeyManager
	Actions              []Action
	Shortcuts            []Shortcut
	ToggleHotkey         *ToggleHotkey
	PollInterval         time.Duration
	Logger               *log.Logger
	AccessibilityCheck   func() bool
	ScreenRecordingCheck func() bool
}

// New constructs an Engine. Required dependencies are validated upfront
// so configuration errors surface at startup, not at first hotkey press.
func New(cfg Config) (*Engine, error) {
	if cfg.WindowManager == nil {
		return nil, fmt.Errorf("engine: WindowManager is required")
	}
	if cfg.HotkeyManager == nil {
		return nil, fmt.Errorf("engine: HotkeyManager is required")
	}
	if len(cfg.Actions) == 0 {
		return nil, fmt.Errorf("engine: at least one action is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	e := &Engine{
		wmgr:         cfg.WindowManager,
		hkmgr:        cfg.HotkeyManager,
		actions:      cfg.Actions,
		shortcuts:    cfg.Shortcuts,
		toggle:       cfg.ToggleHotkey,
		actByID:      make(map[string]*Action, len(cfg.Actions)),
		pollInterval: cfg.PollInterval,
		logger:       cfg.Logger,
		axCheck:      cfg.AccessibilityCheck,
		srCheck:      cfg.ScreenRecordingCheck,
	}
	if e.axCheck != nil {
		e.axGranted.Store(e.axCheck())
	}
	if e.srCheck != nil {
		e.srGranted.Store(e.srCheck())
	}
	for i := range e.actions {
		a := &e.actions[i]
		if _, dup := e.actByID[a.ID]; dup {
			return nil, fmt.Errorf("engine: duplicate action id %q", a.ID)
		}
		e.actByID[a.ID] = a
	}
	for _, s := range cfg.Shortcuts {
		if _, ok := e.actByID[s.ActionID]; !ok {
			return nil, fmt.Errorf("engine: shortcut references unknown action %q", s.ActionID)
		}
	}
	return e, nil
}

// Run starts the engine: registers every shortcut, registers the
// persistent toggle hotkey (if any), starts the hotkey listener, and
// launches the permission polling goroutine (AX / Screen Recording).
//
// It blocks only briefly during setup; the polling goroutine is owned
// by the supplied context. Cancel ctx to shut down cleanly.
func (e *Engine) Run(ctx context.Context) error {
	for _, s := range e.shortcuts {
		s := s
		act := e.actByID[s.ActionID]
		err := e.hkmgr.Register(s.Modifiers, s.Key, func() {
			e.dispatch(act)
		})
		if err != nil {
			return fmt.Errorf("engine: register %s+%s for %s: %w", s.Modifiers, s.Key, s.ActionID, err)
		}
	}
	if e.toggle != nil {
		if err := e.hkmgr.RegisterPersistent(e.toggle.Modifiers, e.toggle.Key, func() {
			e.ToggleUserSuspended()
		}); err != nil {
			return fmt.Errorf("engine: register toggle hotkey: %w", err)
		}
	}
	if err := e.hkmgr.Start(); err != nil {
		return fmt.Errorf("engine: hotkey listener: %w", err)
	}
	go e.permissionPoller(ctx)
	return nil
}

// Stop releases all registered hotkeys.
func (e *Engine) Stop() { e.hkmgr.Stop() }

// dispatch is the suspension-aware execution path used by the hotkey
// callbacks. Per PRD §4.5 it short-circuits when the engine is not
// effectively active.
//
// Note: even though we dynamically unregister hotkeys when suspended, we
// keep the in-handler check as defense-in-depth if a key fires during a
// brief race with SetSuspended.
//
// On error: logs via the engine logger AND records the message on the
// snapshot so the tray can surface it. Also re-checks AX permission
// because the most common cause of a sudden action failure is TCC
// silently revoking accessibility (e.g. after a rebuild).
func (e *Engine) dispatch(a *Action) {
	if !e.effectivelyActive() {
		return
	}
	if err := a.Run(e.wmgr); err != nil {
		e.logger.Printf("action %s failed: %v", a.ID, err)
		e.recordError(fmt.Sprintf("%s: %v", a.ID, err))
		if e.axCheck != nil {
			e.axGranted.Store(e.axCheck())
		}
		e.applySuspension("action error")
	} else {
		e.recordError("")
	}
}

// recordError stores the most recent action failure for Snapshot to
// expose. Empty string clears it.
func (e *Engine) recordError(msg string) {
	e.lastErrMu.Lock()
	e.lastErr = msg
	e.lastErrMu.Unlock()
}

// effectivelyActive applies the user-mode override.
func (e *Engine) effectivelyActive() bool {
	switch UserMode(e.userMode.Load()) {
	case UserModeForceActive:
		return true
	case UserModeForceSuspended:
		return false
	default:
		return true
	}
}

// Execute runs an action by ID, bypassing the suspension middleware.
//
// Used by the tray UI: when a user explicitly clicks a menu item we
// honor the request regardless of suspension (the user has expressed clear intent).
func (e *Engine) Execute(actionID string) error {
	e.mu.RLock()
	a, ok := e.actByID[actionID]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("engine: unknown action %q", actionID)
	}
	if err := a.Run(e.wmgr); err != nil {
		return fmt.Errorf("execute %s: %w", actionID, err)
	}
	return nil
}

// SetUserMode sets the explicit user-mode override and recomputes the
// effective suspension state.
//
// Use ToggleUserSuspended for the standard tray/hotkey toggle behaviour;
// SetUserMode is exposed for tests and future programmatic control.
func (e *Engine) SetUserMode(mode UserMode) {
	e.userMode.Store(int32(mode))
	e.applySuspension(fmt.Sprintf("user mode -> %s", mode))
}

// SetUserSuspended provides the legacy boolean interface: true sets
// ForceSuspended, false sets ForceActive. Auto can only be reached via
// SetUserMode(UserModeAuto).
//
// Kept for backward-compatible callers and the existing test surface.
func (e *Engine) SetUserSuspended(s bool) {
	if s {
		e.SetUserMode(UserModeForceSuspended)
	} else {
		e.SetUserMode(UserModeForceActive)
	}
}

// ToggleUserSuspended flips the effective suspension state via the
// user-mode override. The toggle ALWAYS produces a visible change:
//
//   - If GWiM is currently active, the toggle force-suspends it.
//   - If GWiM is currently suspended, the toggle force-activates it.
func (e *Engine) ToggleUserSuspended() {
	if e.effectivelyActive() {
		e.SetUserMode(UserModeForceSuspended)
	} else {
		e.SetUserMode(UserModeForceActive)
	}
}

// Actions exposes the registered actions in display order. The slice is
// safe to read but should NOT be mutated.
func (e *Engine) Actions() []Action { return e.actions }

// Shortcuts exposes the registered shortcut bindings. Read-only.
func (e *Engine) Shortcuts() []Shortcut { return e.shortcuts }

// Snapshot returns the current suspension state.
func (e *Engine) Snapshot() SuspensionState {
	appID, _ := e.wmgr.GetActiveAppIdentifier()
	mode := UserMode(e.userMode.Load())
	e.lastErrMu.Lock()
	lastErr := e.lastErr
	e.lastErrMu.Unlock()
	return SuspensionState{
		UserMode:               mode,
		UserSuspended:          mode == UserModeForceSuspended,
		ActiveAppID:            appID,
		ManagedHotkeyCount:     len(e.shortcuts),
		AccessibilityChecked:   e.axCheck != nil,
		AccessibilityGranted:   e.axGranted.Load(),
		ScreenRecordingChecked: e.srCheck != nil,
		ScreenRecordingGranted: e.srGranted.Load(),
		LastActionError:        lastErr,
	}
}

// RefreshAccessibility re-runs the AX check (without prompting) and
// notifies listeners if the state changed. Useful after the user has
// just toggled the permission in System Settings.
func (e *Engine) RefreshAccessibility() {
	if e.axCheck == nil {
		return
	}
	prev := e.axGranted.Load()
	now := e.axCheck()
	if prev != now {
		e.axGranted.Store(now)
		e.applySuspension(fmt.Sprintf("accessibility -> %t", now))
	}
}

// RefreshScreenRecording re-runs the Screen Recording check (without
// prompting) and notifies listeners if the state changed.
func (e *Engine) RefreshScreenRecording() {
	if e.srCheck == nil {
		return
	}
	prev := e.srGranted.Load()
	now := e.srCheck()
	if prev != now {
		e.srGranted.Store(now)
		e.applySuspension(fmt.Sprintf("screen-recording -> %t", now))
	}
}

// AddListener subscribes a callback for suspension-state changes. The
// callback runs on the permission poller goroutine (and from applySuspension),
// so it must be cheap and non-blocking; long work should be queued.
func (e *Engine) AddListener(fn func(SuspensionState)) {
	e.listenerMu.Lock()
	e.listeners = append(e.listeners, fn)
	e.listenerMu.Unlock()
}

// permissionPoller periodically refreshes accessibility and screen-recording
// grants when configured, so the tray reflects System Settings changes
// within PollInterval.
func (e *Engine) permissionPoller(ctx context.Context) {
	t := time.NewTicker(e.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			changed := false
			reason := ""

			if e.axCheck != nil {
				granted := e.axCheck()
				if e.axGranted.Load() != granted {
					e.axGranted.Store(granted)
					changed = true
					reason = fmt.Sprintf("accessibility -> %t", granted)
				}
			}
			if e.srCheck != nil {
				granted := e.srCheck()
				if e.srGranted.Load() != granted {
					e.srGranted.Store(granted)
					changed = true
					if reason == "" {
						reason = fmt.Sprintf("screen-recording -> %t", granted)
					}
				}
			}
			if changed {
				e.applySuspension(reason)
			}
		}
	}
}

// applySuspension propagates the effective suspension state to the
// HotkeyManager and broadcasts to listeners.
//
// "Effective" means the result of user mode — see effectivelyActive.
func (e *Engine) applySuspension(reason string) {
	suspended := !e.effectivelyActive()
	if e.hkmgr.Suspended() != suspended {
		e.hkmgr.SetSuspended(suspended)
		e.logger.Printf("hotkeys suspended=%t (%s)", suspended, reason)
	}
	snap := e.Snapshot()
	e.listenerMu.Lock()
	listeners := make([]func(SuspensionState), len(e.listeners))
	copy(listeners, e.listeners)
	e.listenerMu.Unlock()
	for _, fn := range listeners {
		fn(snap)
	}
}
