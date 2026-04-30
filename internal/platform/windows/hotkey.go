//go:build windows

package windows

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/nealhardesty/gwim/internal/wm"
)

// Hotkey-loop binding surface. RegisterHotKey is thread-bound on Windows
// (WM_HOTKEY only arrives on the queue of the thread that called
// RegisterHotKey), so we run a dedicated OS-thread-locked goroutine that
// owns every binding.
var (
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")

	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// Win32 message identifiers we listen for. WM_APP+N is reserved by
// Microsoft for application-private messages; we use a small range to
// drive register / unregister / suspend / quit from other goroutines.
const (
	wmHotkey   = 0x0312
	wmApp      = 0x8000
	wmRegister = wmApp + 1
	wmRegAll   = wmApp + 2 // re-register every binding (resume from suspend)
	wmUnregAll = wmApp + 3 // unregister every regular binding (suspend)
	wmQuit     = wmApp + 4
)

// msg mirrors the Win32 MSG struct we marshal to GetMessageW.
type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// hkBinding is a single registered key combination.
type hkBinding struct {
	id         uint32 // matches the WPARAM Win32 sends in WM_HOTKEY
	modifiers  uint32
	keycode    uint32
	handler    wm.HotkeyHandler
	persistent bool
	// registered tracks whether the OS currently has this binding live.
	// SetSuspended(true) clears it on regular bindings so the keys flow to
	// the foreground app; SetSuspended(false) restores them.
	registered bool
	descLabel  string
}

// winHotkeyManager is the Windows implementation of wm.HotkeyManager.
//
// Suspension semantics match macOS:
//   - Regular hotkeys (Register) are physically unregistered on
//     SetSuspended(true) so the keys reach the foreground app unhandled.
//   - Persistent hotkeys (RegisterPersistent) stay registered at all
//     times so e.g. Ctrl+Alt+X can always toggle GWiM back on.
type winHotkeyManager struct {
	mu        sync.Mutex
	bindings  map[uint32]*hkBinding
	nextID    uint32
	suspended bool
	started   bool
	threadID  uint32
	ready     chan struct{}
	stopped   chan struct{}

	// dispatchTable resolves binding ID → handler outside the message loop
	// so a slow handler can't block subsequent WM_HOTKEY events.
	dispatchTable atomic.Value // map[uint32]wm.HotkeyHandler
}

// NewHotkeyManager constructs a Windows-backed HotkeyManager.
func NewHotkeyManager() wm.HotkeyManager {
	m := &winHotkeyManager{
		bindings: make(map[uint32]*hkBinding),
		ready:    make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	m.dispatchTable.Store(map[uint32]wm.HotkeyHandler{})
	return m
}

// Register adds a regular (suspendable) hotkey.
func (m *winHotkeyManager) Register(modifiers []string, key string, handler wm.HotkeyHandler) error {
	return m.register(modifiers, key, handler, false)
}

// RegisterPersistent adds a hotkey immune to SetSuspended.
func (m *winHotkeyManager) RegisterPersistent(modifiers []string, key string, handler wm.HotkeyHandler) error {
	return m.register(modifiers, key, handler, true)
}

func (m *winHotkeyManager) register(modifiers []string, key string, handler wm.HotkeyHandler, persistent bool) error {
	if handler == nil {
		return fmt.Errorf("hotkey: nil handler for %v + %s", modifiers, key)
	}
	mask, err := modifierMaskFor(modifiers)
	if err != nil {
		return fmt.Errorf("hotkey: %w (modifiers=%v)", err, modifiers)
	}
	keycode, err := keycodeFor(key)
	if err != nil {
		return fmt.Errorf("hotkey: %w (key=%q)", err, key)
	}

	m.mu.Lock()
	m.nextID++
	b := &hkBinding{
		id:         m.nextID,
		modifiers:  mask,
		keycode:    keycode,
		handler:    handler,
		persistent: persistent,
		descLabel:  fmt.Sprintf("%v+%s", modifiers, key),
	}
	m.bindings[b.id] = b
	m.refreshDispatchTable()
	started := m.started
	threadID := m.threadID
	suspended := m.suspended
	m.mu.Unlock()

	// If Start hasn't run yet the goroutine will register everything
	// itself when it boots. After Start, we ask the loop to register
	// just this new binding via WM_REGISTER+id.
	if !started {
		return nil
	}
	if !persistent && suspended {
		// Stays dormant until SetSuspended(false) re-registers.
		return nil
	}
	if !postThreadMessage(threadID, wmRegister, uintptr(b.id), 0) {
		return fmt.Errorf("hotkey: failed to enqueue registration for %s", b.descLabel)
	}
	return nil
}

// Start spawns the dedicated message-pump goroutine. Subsequent calls
// are no-ops (matching macHotkeyManager.Start).
func (m *winHotkeyManager) Start() error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.mu.Unlock()

	go m.runLoop()

	// Block until the goroutine has acquired its thread ID — otherwise
	// concurrent Register calls would post into a zero threadID and
	// silently no-op.
	<-m.ready
	return nil
}

// Stop posts WM_QUIT to the loop goroutine and waits for it to exit.
func (m *winHotkeyManager) Stop() {
	m.mu.Lock()
	threadID := m.threadID
	started := m.started
	m.mu.Unlock()
	if !started || threadID == 0 {
		return
	}
	postThreadMessage(threadID, wmQuit, 0, 0)
	<-m.stopped
}

// SetSuspended toggles registration of regular bindings.
func (m *winHotkeyManager) SetSuspended(s bool) {
	m.mu.Lock()
	if m.suspended == s {
		m.mu.Unlock()
		return
	}
	m.suspended = s
	threadID := m.threadID
	started := m.started
	m.mu.Unlock()

	if !started || threadID == 0 {
		return
	}
	if s {
		postThreadMessage(threadID, wmUnregAll, 0, 0)
	} else {
		postThreadMessage(threadID, wmRegAll, 0, 0)
	}
}

// Suspended reports the current suspension state.
func (m *winHotkeyManager) Suspended() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.suspended
}

// =====================================================================
// Internal: message-pump goroutine.
// =====================================================================

// runLoop is the dedicated OS-thread message pump. It owns:
//   - registration of every binding (RegisterHotKey is thread-affined),
//   - the WM_HOTKEY dispatch loop,
//   - register/unregister/suspend/quit reactions to PostThreadMessage,
//
// All runs from the same OS thread for the lifetime of the manager.
func (m *winHotkeyManager) runLoop() {
	runtime.LockOSThread()
	// We deliberately do NOT UnlockOSThread on exit — the goroutine ends
	// here, so the thread terminates with us.

	tid, _, _ := procGetCurrentThreadId.Call()
	m.mu.Lock()
	m.threadID = uint32(tid)
	suspended := m.suspended
	bindings := m.snapshotBindingsLocked()
	m.mu.Unlock()
	close(m.ready)

	// Initial registration of any bindings queued before Start.
	for _, b := range bindings {
		if !b.persistent && suspended {
			continue
		}
		m.registerOne(b)
	}

	defer close(m.stopped)

	var msgBuf msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msgBuf)), 0, 0, 0)
		// GetMessageW returns 0 on WM_QUIT and -1 (== 0xFFFFFFFF on 32-bit
		// or 0xFFFFFFFFFFFFFFFF on 64-bit) on error. We treat anything
		// non-positive-and-non-WM_QUIT-ish as "exit".
		if int32(ret) <= 0 {
			break
		}

		switch msgBuf.Message {
		case wmHotkey:
			id := uint32(msgBuf.WParam)
			if h := m.lookupHandler(id); h != nil {
				go h() // run off the message loop so a slow handler can't queue up keys
			}
		case wmRegister:
			id := uint32(msgBuf.WParam)
			m.mu.Lock()
			b := m.bindings[id]
			m.mu.Unlock()
			if b != nil {
				m.registerOne(b)
			}
		case wmRegAll:
			m.mu.Lock()
			snap := m.snapshotBindingsLocked()
			m.mu.Unlock()
			for _, b := range snap {
				if !b.registered && !b.persistent {
					m.registerOne(b)
				}
			}
		case wmUnregAll:
			m.mu.Lock()
			snap := m.snapshotBindingsLocked()
			m.mu.Unlock()
			for _, b := range snap {
				if b.registered && !b.persistent {
					m.unregisterOne(b)
				}
			}
		case wmQuit:
			// Unregister everything before tearing down so the OS isn't
			// left holding zombie hotkey bindings.
			m.mu.Lock()
			snap := m.snapshotBindingsLocked()
			m.mu.Unlock()
			for _, b := range snap {
				if b.registered {
					m.unregisterOne(b)
				}
			}
			return
		default:
			// Pass non-hotkey messages through the standard pipeline so
			// the system stays responsive (this thread is otherwise
			// idle, so this is mostly defensive).
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msgBuf)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msgBuf)))
		}
	}
}

// snapshotBindingsLocked returns a deterministic slice of pointers to
// the live bindings — must be called with m.mu held.
func (m *winHotkeyManager) snapshotBindingsLocked() []*hkBinding {
	out := make([]*hkBinding, 0, len(m.bindings))
	for _, b := range m.bindings {
		out = append(out, b)
	}
	return out
}

// registerOne calls RegisterHotKey for one binding. Must be called from
// the message-pump thread because RegisterHotKey is thread-affined.
func (m *winHotkeyManager) registerOne(b *hkBinding) {
	ret, _, _ := procRegisterHotKey.Call(0, uintptr(b.id), uintptr(b.modifiers), uintptr(b.keycode))
	if ret == 0 {
		// Conflict (e.g. Win+L is reserved by Windows lock screen). Log
		// via the engine logger isn't reachable from here; the engine
		// will surface failed actions to the tray instead. We mark it
		// unregistered so a future SetSuspended(false) doesn't try
		// double-registration.
		b.registered = false
		return
	}
	b.registered = true
}

// unregisterOne is the inverse of registerOne.
func (m *winHotkeyManager) unregisterOne(b *hkBinding) {
	procUnregisterHotKey.Call(0, uintptr(b.id))
	b.registered = false
}

// refreshDispatchTable swaps in a new id→handler map so the WM_HOTKEY
// branch can resolve handlers without taking m.mu.
//
// Must be called with m.mu already held.
func (m *winHotkeyManager) refreshDispatchTable() {
	out := make(map[uint32]wm.HotkeyHandler, len(m.bindings))
	for id, b := range m.bindings {
		out[id] = b.handler
	}
	m.dispatchTable.Store(out)
}

func (m *winHotkeyManager) lookupHandler(id uint32) wm.HotkeyHandler {
	tbl, _ := m.dispatchTable.Load().(map[uint32]wm.HotkeyHandler)
	return tbl[id]
}

// postThreadMessage is a thin wrapper around PostThreadMessageW that
// returns whether the post succeeded.
func postThreadMessage(threadID uint32, message uint32, wparam, lparam uintptr) bool {
	ret, _, _ := procPostThreadMessageW.Call(
		uintptr(threadID),
		uintptr(message),
		wparam,
		lparam,
	)
	return ret != 0
}
