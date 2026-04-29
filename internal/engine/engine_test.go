package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nealhardesty/gwim/internal/wm"
)

// fakeWindow is a stub wm.Window that records SetFrame calls. We only use
// it for tests that need a target — the engine never inspects its internal
// state directly.
type fakeWindow struct {
	frame wm.Rect
	calls int32
}

func (w *fakeWindow) GetFrame() (wm.Rect, error) { return w.frame, nil }
func (w *fakeWindow) SetFrame(r wm.Rect) error   { w.frame = r; atomic.AddInt32(&w.calls, 1); return nil }
func (w *fakeWindow) ToggleFullScreen() error    { return nil }

// fakeWM is the in-memory wm.WindowManager used in engine tests.
//
// activeApp is mutable from tests to simulate switching foreground apps.
type fakeWM struct {
	mu        sync.Mutex
	win       *fakeWindow
	screen    wm.Rect
	activeApp string
}

func (f *fakeWM) GetActiveWindow() (wm.Window, error)                        { return f.win, nil }
func (f *fakeWM) GetScreenFrame(_ wm.Window) (wm.Rect, error)                { return f.screen, nil }
func (f *fakeWM) MoveWindowToScreen(_ wm.Window, _ wm.ScreenDirection) error { return nil }
func (f *fakeWM) GetActiveAppIdentifier() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeApp, nil
}
func (f *fakeWM) setActiveApp(id string) {
	f.mu.Lock()
	f.activeApp = id
	f.mu.Unlock()
}

// fakeHK is the in-memory wm.HotkeyManager used in engine tests.
//
// It distinguishes regular vs persistent bindings so tests can assert
// that persistent hotkeys remain dispatchable while suspended (the
// production macOS implementation does the same by physically leaving
// persistent bindings registered with Carbon).
type fakeHK struct {
	mu         sync.Mutex
	regular    map[string]wm.HotkeyHandler
	persistent map[string]wm.HotkeyHandler
	suspended  bool
	started    bool
}

func newFakeHK() *fakeHK {
	return &fakeHK{
		regular:    map[string]wm.HotkeyHandler{},
		persistent: map[string]wm.HotkeyHandler{},
	}
}

func (h *fakeHK) Register(mods []string, key string, handler wm.HotkeyHandler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.regular[key] = handler
	return nil
}
func (h *fakeHK) RegisterPersistent(mods []string, key string, handler wm.HotkeyHandler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.persistent[key] = handler
	return nil
}
func (h *fakeHK) Start() error        { h.started = true; return nil }
func (h *fakeHK) Stop()               { h.started = false }
func (h *fakeHK) SetSuspended(b bool) { h.mu.Lock(); h.suspended = b; h.mu.Unlock() }
func (h *fakeHK) Suspended() bool     { h.mu.Lock(); defer h.mu.Unlock(); return h.suspended }

// fire simulates the OS dispatching a registered hotkey. Regular hotkeys
// are skipped while suspended (matching the macOS dynamic-unregister
// behaviour); persistent hotkeys always fire.
func (h *fakeHK) fire(key string) {
	h.mu.Lock()
	if handler, ok := h.persistent[key]; ok {
		h.mu.Unlock()
		handler()
		return
	}
	if h.suspended {
		h.mu.Unlock()
		return
	}
	handler := h.regular[key]
	h.mu.Unlock()
	if handler != nil {
		handler()
	}
}

// makeEngine returns a small two-action engine wired to the supplied
// fakes plus a single shortcut bound to action "snap.left".
func makeEngine(t *testing.T, fwm *fakeWM, fhk *fakeHK) *Engine {
	t.Helper()
	actions := []Action{
		snapAction("snap.left", "Snap Left", "Test", snapLeftHalf),
	}
	shortcuts := []Shortcut{
		{ActionID: "snap.left", Modifiers: []string{"ctrl", "alt"}, Key: "h"},
	}
	e, err := New(Config{
		WindowManager: fwm,
		HotkeyManager: fhk,
		Actions:       actions,
		Shortcuts:     shortcuts,
		PollInterval:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

// TestDispatch_Active verifies the happy path: a fired hotkey runs the
// action against the active window.
func TestDispatch_Active(t *testing.T) {
	win := &fakeWindow{frame: wm.Rect{X: 50, Y: 50, W: 400, H: 400}}
	fwm := &fakeWM{win: win, screen: wm.Rect{X: 0, Y: 0, W: 1000, H: 800}, activeApp: "com.test.allowed"}
	fhk := newFakeHK()
	e := makeEngine(t, fwm, fhk)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer e.Stop()

	fhk.fire("h")
	if got := atomic.LoadInt32(&win.calls); got != 1 {
		t.Fatalf("expected 1 SetFrame call, got %d", got)
	}
	if win.frame.W != 500 {
		t.Fatalf("expected left-half (W=500), got %+v", win.frame)
	}
}

// TestDispatch_UserSuspended verifies the manual tray toggle blocks dispatch.
func TestDispatch_UserSuspended(t *testing.T) {
	win := &fakeWindow{}
	fwm := &fakeWM{win: win, screen: wm.Rect{W: 1000, H: 800}, activeApp: "com.test.allowed"}
	fhk := newFakeHK()
	e := makeEngine(t, fwm, fhk)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	e.SetUserSuspended(true)
	fhk.fire("h")
	if got := atomic.LoadInt32(&win.calls); got != 0 {
		t.Fatalf("user-suspended dispatch should be blocked; got %d calls", got)
	}
	if !fhk.Suspended() {
		t.Fatalf("HotkeyManager should reflect suspended state")
	}
}

// TestExecute_BypassesSuspension checks the tray's click path: even when
// suspended, an explicit Execute call runs the action (PRD §3.2).
func TestExecute_BypassesSuspension(t *testing.T) {
	win := &fakeWindow{frame: wm.Rect{W: 100, H: 100}}
	fwm := &fakeWM{win: win, screen: wm.Rect{W: 1000, H: 800}, activeApp: "com.test.blocked"}
	fhk := newFakeHK()
	e := makeEngine(t, fwm, fhk)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	e.SetUserSuspended(true)
	if err := e.Execute("snap.left"); err != nil {
		t.Fatalf("Execute should succeed even when suspended: %v", err)
	}
	if got := atomic.LoadInt32(&win.calls); got != 1 {
		t.Fatalf("expected 1 SetFrame call from Execute, got %d", got)
	}
}

// TestNew_DuplicateActionID guards the action-table validator.
func TestNew_DuplicateActionID(t *testing.T) {
	_, err := New(Config{
		WindowManager: &fakeWM{},
		HotkeyManager: newFakeHK(),
		Actions: []Action{
			{ID: "dup", Run: func(wm.WindowManager) error { return nil }},
			{ID: "dup", Run: func(wm.WindowManager) error { return nil }},
		},
	})
	if err == nil {
		t.Fatal("expected error for duplicate action id")
	}
}

// TestNew_UnknownShortcut guards against typos in the shortcut table.
func TestNew_UnknownShortcut(t *testing.T) {
	_, err := New(Config{
		WindowManager: &fakeWM{},
		HotkeyManager: newFakeHK(),
		Actions: []Action{
			{ID: "ok", Run: func(wm.WindowManager) error { return nil }},
		},
		Shortcuts: []Shortcut{
			{ActionID: "typo", Modifiers: []string{"ctrl"}, Key: "a"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown shortcut action id")
	}
}

// TestPrimaryShortcutFor checks the formatting helper used by the tray.
func TestPrimaryShortcutFor(t *testing.T) {
	shortcuts := []Shortcut{
		{ActionID: "snap.left", Modifiers: []string{"ctrl", "alt"}, Key: "left"},
		{ActionID: "snap.left", Modifiers: []string{"ctrl", "alt"}, Key: "h"},
	}
	got := PrimaryShortcutFor("snap.left", shortcuts)
	if got != "⌃⌥←" {
		t.Fatalf("unexpected primary accelerator: %q", got)
	}
	if PrimaryShortcutFor("missing", shortcuts) != "" {
		t.Fatal("expected empty string for unknown action")
	}
}

// TestToggle_FlipsActiveAndSuspended checks the basic two-state cycle
// of the user-mode toggle from the default Auto state.
func TestToggle_FlipsActiveAndSuspended(t *testing.T) {
	win := &fakeWindow{}
	fwm := &fakeWM{win: win, screen: wm.Rect{W: 1000, H: 800}, activeApp: "com.test.allowed"}
	fhk := newFakeHK()
	e := makeEngine(t, fwm, fhk)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	if !e.Snapshot().Active() {
		t.Fatal("expected active by default")
	}

	e.ToggleUserSuspended()
	s := e.Snapshot()
	if s.UserMode != UserModeForceSuspended || s.Active() {
		t.Fatalf("toggle should suspend; got mode=%s active=%t", s.UserMode, s.Active())
	}

	e.ToggleUserSuspended()
	s = e.Snapshot()
	if s.UserMode != UserModeForceActive || !s.Active() {
		t.Fatalf("re-toggle should re-activate; got mode=%s active=%t", s.UserMode, s.Active())
	}
}

// TestPersistentHotkey_FiresWhileSuspended confirms a hotkey registered
// via RegisterPersistent dispatches even while regular hotkeys do not.
// This is what makes the Ctrl+Alt+X toggle reachable while suspended.
func TestPersistentHotkey_FiresWhileSuspended(t *testing.T) {
	fwm := &fakeWM{win: &fakeWindow{}, screen: wm.Rect{W: 1000, H: 800}, activeApp: "com.test.allowed"}
	fhk := newFakeHK()

	actions := []Action{
		snapAction("snap.left", "Snap Left", "Test", snapLeftHalf),
	}
	shortcuts := []Shortcut{
		{ActionID: "snap.left", Modifiers: []string{"ctrl", "alt"}, Key: "h"},
	}
	e, err := New(Config{
		WindowManager: fwm,
		HotkeyManager: fhk,
		Actions:       actions,
		Shortcuts:     shortcuts,
		ToggleHotkey:  &ToggleHotkey{Modifiers: []string{"ctrl", "alt"}, Key: "x"},
		PollInterval:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	// Force-suspend, then verify regular hotkey is blocked but the
	// persistent toggle still flips state.
	e.SetUserMode(UserModeForceSuspended)
	if e.Snapshot().Active() {
		t.Fatal("setup: expected suspended after SetUserMode(ForceSuspended)")
	}

	fhk.fire("h")
	if got := atomic.LoadInt32(&fwm.win.calls); got != 0 {
		t.Fatalf("regular hotkey should be blocked while suspended; got %d", got)
	}

	fhk.fire("x") // persistent — must fire and toggle state back to active
	if !e.Snapshot().Active() {
		t.Fatalf("persistent toggle hotkey should re-activate; got %+v", e.Snapshot())
	}
}

// TestDefaultToggleHotkey ensures the canonical accelerator stays at Ctrl+Alt+X.
func TestDefaultToggleHotkey(t *testing.T) {
	tk := DefaultToggleHotkey()
	if tk == nil {
		t.Fatal("DefaultToggleHotkey returned nil")
	}
	if tk.Key != "x" {
		t.Errorf("key: got %q want %q", tk.Key, "x")
	}
	if got := tk.Format(); got != "⌃⌥X" {
		t.Errorf("Format: got %q want %q", got, "⌃⌥X")
	}
}

// TestDefaultShortcutsMatchActions guards the canonical shortcut table.
func TestDefaultShortcutsMatchActions(t *testing.T) {
	actions := DefaultActions()
	ids := make(map[string]bool, len(actions))
	for _, a := range actions {
		ids[a.ID] = true
	}
	for _, s := range DefaultShortcuts() {
		if !ids[s.ActionID] {
			t.Errorf("shortcut references unknown action id: %s", s.ActionID)
		}
	}
}
