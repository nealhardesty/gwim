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

// Engine is the heart of GWiM: it holds the action table, the live
// blocklist, and the two suspension axes (manual user toggle + automatic
// blocklist match).
//
// It deliberately exposes a tiny surface (Run/Execute/SetUserSuspended/
// Stop/Snapshot) so the UI and main packages stay decoupled.
type Engine struct {
	wmgr      wm.WindowManager
	hkmgr     wm.HotkeyManager
	actions   []Action
	shortcuts []Shortcut
	blocklist []string

	mu      sync.RWMutex
	actByID map[string]*Action

	userSuspended atomic.Bool // toggled by tray
	autoSuspended atomic.Bool // toggled by blocklist poller

	// Listeners notified whenever combined suspension state changes,
	// allowing the tray UI to refresh its checkmarks without polling.
	listenerMu sync.Mutex
	listeners  []func(state SuspensionState)

	pollInterval time.Duration
	logger       *log.Logger
}

// SuspensionState is a snapshot of the engine's suspension axes plus the
// active app identifier the engine last observed. The tray UI consumes
// these to render its menu state.
type SuspensionState struct {
	UserSuspended      bool
	AutoSuspended      bool
	ActiveAppID        string
	ActiveAppBlocked   bool
	ManagedHotkeyCount int
}

// Active reports whether GWiM is currently dispatching hotkeys.
func (s SuspensionState) Active() bool {
	return !s.UserSuspended && !s.AutoSuspended
}

// Config bundles the dependencies and tunables for a new Engine.
//
// PollInterval defaults to 500ms when zero — fast enough that the user
// can switch to a remote desktop and immediately type without GWiM
// stealing the first key, slow enough not to burn CPU.
type Config struct {
	WindowManager wm.WindowManager
	HotkeyManager wm.HotkeyManager
	Actions       []Action
	Shortcuts     []Shortcut
	Blocklist     []string
	PollInterval  time.Duration
	Logger        *log.Logger
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
		blocklist:    cfg.Blocklist,
		actByID:      make(map[string]*Action, len(cfg.Actions)),
		pollInterval: cfg.PollInterval,
		logger:       cfg.Logger,
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

// Run starts the engine: registers every shortcut, starts the hotkey
// listener, and launches the blocklist polling goroutine.
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
	if err := e.hkmgr.Start(); err != nil {
		return fmt.Errorf("engine: hotkey listener: %w", err)
	}
	go e.blocklistPoller(ctx)
	return nil
}

// Stop releases all registered hotkeys.
func (e *Engine) Stop() { e.hkmgr.Stop() }

// dispatch is the suspension-aware execution path used by the hotkey
// callbacks. Per PRD §4.5 it short-circuits when either suspension axis
// is active.
//
// Note: even though we dynamically unregister hotkeys when suspended, we
// keep the in-handler check as defense-in-depth in case a key fires
// during the small window between blocklist app activation and the next
// poll tick.
func (e *Engine) dispatch(a *Action) {
	if e.userSuspended.Load() || e.autoSuspended.Load() {
		return
	}
	if err := a.Run(e.wmgr); err != nil {
		e.logger.Printf("action %s failed: %v", a.ID, err)
	}
}

// Execute runs an action by ID, bypassing the suspension middleware.
//
// Used by the tray UI: when a user explicitly clicks a menu item we
// honor the request even if the foreground app is in the blocklist
// (the user has expressed clear intent).
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

// SetUserSuspended is invoked by the tray's Suspend toggle. Recomputes
// the combined suspension state and notifies listeners.
func (e *Engine) SetUserSuspended(s bool) {
	e.userSuspended.Store(s)
	e.applySuspension("user toggle")
}

// Actions exposes the registered actions in display order. The slice is
// safe to read but should NOT be mutated.
func (e *Engine) Actions() []Action { return e.actions }

// Shortcuts exposes the registered shortcut bindings. Read-only.
func (e *Engine) Shortcuts() []Shortcut { return e.shortcuts }

// Snapshot returns the current suspension state.
func (e *Engine) Snapshot() SuspensionState {
	appID, _ := e.wmgr.GetActiveAppIdentifier()
	return SuspensionState{
		UserSuspended:      e.userSuspended.Load(),
		AutoSuspended:      e.autoSuspended.Load(),
		ActiveAppID:        appID,
		ActiveAppBlocked:   e.isBlocked(appID),
		ManagedHotkeyCount: len(e.shortcuts),
	}
}

// AddListener subscribes a callback for suspension-state changes. The
// callback runs on the engine's poller goroutine, so it must be cheap
// and non-blocking; long work should be queued.
func (e *Engine) AddListener(fn func(SuspensionState)) {
	e.listenerMu.Lock()
	e.listeners = append(e.listeners, fn)
	e.listenerMu.Unlock()
}

// blocklistPoller periodically checks the foreground application and
// updates auto-suspension. We use polling rather than NSWorkspace
// notifications to keep the cgo surface small and the architecture
// portable to Windows (where GetForegroundWindow is the canonical poll).
func (e *Engine) blocklistPoller(ctx context.Context) {
	t := time.NewTicker(e.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			appID, err := e.wmgr.GetActiveAppIdentifier()
			if err != nil {
				continue
			}
			blocked := e.isBlocked(appID)
			if e.autoSuspended.Load() != blocked {
				e.autoSuspended.Store(blocked)
				e.applySuspension(fmt.Sprintf("active app=%q blocked=%t", appID, blocked))
			}
		}
	}
}

// applySuspension propagates the combined suspension state to the
// HotkeyManager and broadcasts to listeners.
func (e *Engine) applySuspension(reason string) {
	combined := e.userSuspended.Load() || e.autoSuspended.Load()
	if e.hkmgr.Suspended() != combined {
		e.hkmgr.SetSuspended(combined)
		e.logger.Printf("hotkeys suspended=%t (%s)", combined, reason)
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

// isBlocked reports whether the supplied app identifier matches the
// hardcoded blocklist (case-insensitive equality on the bundle ID).
func (e *Engine) isBlocked(appID string) bool {
	if appID == "" {
		return false
	}
	for _, b := range e.blocklist {
		if equalFold(b, appID) {
			return true
		}
	}
	return false
}

// equalFold is a tiny ASCII case-insensitive comparator. We avoid
// strings.EqualFold to skip the unicode work — bundle IDs are ASCII.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
