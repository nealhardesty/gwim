//go:build darwin

package macos

/*
#import <Carbon/Carbon.h>
#include <stdbool.h>

// Forward declaration of the Go-exported dispatcher. cgo synthesises this
// symbol from the //export directive in hotkey.go.
extern void gwimHotkeyDispatch(unsigned int id);

// gwim_hotkey_event_handler is the single Carbon event handler shared by
// every registered hotkey. It pulls the EventHotKeyID off the event and
// hands the numeric id back to Go so the right handler can be resolved
// without keeping any per-hotkey C state.
static OSStatus gwim_hotkey_event_handler(EventHandlerCallRef nextHandler,
                                          EventRef event, void *userData) {
    EventHotKeyID hkID;
    GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID,
                      NULL, sizeof(EventHotKeyID), NULL, &hkID);
    gwimHotkeyDispatch((unsigned int)hkID.id);
    return noErr;
}

// gwim_install_hotkey_handler wires up the single shared handler. Called
// once during Start().
static OSStatus gwim_install_hotkey_handler(void) {
    EventTypeSpec et;
    et.eventClass = kEventClassKeyboard;
    et.eventKind  = kEventHotKeyPressed;
    return InstallEventHandler(GetApplicationEventTarget(),
                               &gwim_hotkey_event_handler,
                               1, &et, NULL, NULL);
}

// gwim_register_hotkey registers a single hotkey and returns its
// EventHotKeyRef as an opaque pointer (cast to uintptr_t). Returns 0 on
// failure.
static uintptr_t gwim_register_hotkey(unsigned int id, unsigned int keycode,
                                      unsigned int modifiers) {
    EventHotKeyID hkID;
    hkID.signature = 'gwim';
    hkID.id = id;

    EventHotKeyRef ref = NULL;
    OSStatus s = RegisterEventHotKey(keycode, modifiers, hkID,
                                     GetApplicationEventTarget(), 0, &ref);
    if (s != noErr) return 0;
    return (uintptr_t)ref;
}

// gwim_unregister_hotkey releases a previously registered hotkey.
static void gwim_unregister_hotkey(uintptr_t ref) {
    if (ref == 0) return;
    UnregisterEventHotKey((EventHotKeyRef)ref);
}
*/
import "C"

import (
	"fmt"
	"sync"

	"github.com/nealhardesty/gwim/internal/wm"
)

// macHotkeyManager is the macOS implementation of wm.HotkeyManager backed
// by Carbon RegisterEventHotKey.
//
// Suspension semantics: when SetSuspended(true) is called, every hotkey is
// physically unregistered from the OS so the keystroke flows to the
// foreground application. This is required for remote-desktop workflows
// where Ctrl+Alt+arrow must reach the remote machine, not be swallowed by
// GWiM. Re-registration on SetSuspended(false) is best-effort; failures
// are logged but do not abort other hotkeys.
type macHotkeyManager struct {
	mu        sync.Mutex
	bindings  []*hotkeyBinding
	suspended bool
	started   bool
	nextID    uint32
}

// hotkeyBinding represents a single registered combination.
//
// liveRef is the live C-side EventHotKeyRef when the hotkey is active, or
// 0 when it has been unregistered (e.g. while the manager is suspended).
type hotkeyBinding struct {
	id        uint32
	keycode   uint32
	modifiers uint32
	handler   wm.HotkeyHandler
	liveRef   uintptr
	descLabel string // for logging/debug only
}

// NewHotkeyManager constructs a macOS-backed HotkeyManager.
func NewHotkeyManager() wm.HotkeyManager {
	return &macHotkeyManager{}
}

// Register validates the modifier+key combination, allocates a unique ID,
// and stores the binding. The actual Carbon registration happens in
// Start (once the event handler is installed) or immediately if Start has
// already been called.
func (m *macHotkeyManager) Register(modifiers []string, key string, handler wm.HotkeyHandler) error {
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
	defer m.mu.Unlock()
	m.nextID++
	b := &hotkeyBinding{
		id:        m.nextID,
		keycode:   keycode,
		modifiers: mask,
		handler:   handler,
		descLabel: fmt.Sprintf("%v+%s", modifiers, key),
	}
	m.bindings = append(m.bindings, b)

	if m.started && !m.suspended {
		if ref := C.gwim_register_hotkey(C.uint(b.id), C.uint(b.keycode), C.uint(b.modifiers)); ref != 0 {
			b.liveRef = uintptr(ref)
		} else {
			return fmt.Errorf("hotkey: failed to register %s with the OS (already in use?)", b.descLabel)
		}
		registry.add(b.id, b.handler)
	}
	return nil
}

// Start installs the shared Carbon event handler and registers every
// previously stored binding. Safe to call multiple times.
func (m *macHotkeyManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		if status := C.gwim_install_hotkey_handler(); status != 0 {
			return fmt.Errorf("hotkey: InstallEventHandler returned %d", int(status))
		}
		m.started = true
	}
	if m.suspended {
		return nil
	}
	for _, b := range m.bindings {
		if b.liveRef != 0 {
			continue
		}
		ref := C.gwim_register_hotkey(C.uint(b.id), C.uint(b.keycode), C.uint(b.modifiers))
		if ref == 0 {
			return fmt.Errorf("hotkey: failed to register %s (already bound?)", b.descLabel)
		}
		b.liveRef = uintptr(ref)
		registry.add(b.id, b.handler)
	}
	return nil
}

// Stop unregisters every hotkey and clears bindings. Safe to call when not started.
func (m *macHotkeyManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bindings {
		if b.liveRef != 0 {
			C.gwim_unregister_hotkey(C.uintptr_t(b.liveRef))
			b.liveRef = 0
		}
		registry.remove(b.id)
	}
}

// SetSuspended toggles the live registration of all hotkeys. While
// suspended, key combinations propagate to the foreground app unhandled.
func (m *macHotkeyManager) SetSuspended(s bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.suspended == s {
		return
	}
	m.suspended = s
	if !m.started {
		return
	}
	if s {
		for _, b := range m.bindings {
			if b.liveRef != 0 {
				C.gwim_unregister_hotkey(C.uintptr_t(b.liveRef))
				b.liveRef = 0
			}
		}
		return
	}
	for _, b := range m.bindings {
		if b.liveRef != 0 {
			continue
		}
		ref := C.gwim_register_hotkey(C.uint(b.id), C.uint(b.keycode), C.uint(b.modifiers))
		if ref != 0 {
			b.liveRef = uintptr(ref)
			registry.add(b.id, b.handler)
		}
	}
}

// Suspended reports the current suspension state.
func (m *macHotkeyManager) Suspended() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.suspended
}

// hotkeyRegistry is a tiny thread-safe lookup keyed by hotkey ID.
//
// We keep it as a package-level singleton because the C event handler
// gwim_hotkey_event_handler has no convenient way to carry per-instance
// userdata across the cgo boundary, and a single registry suffices for
// the entire process (only one HotkeyManager is created at a time).
type hotkeyRegistry struct {
	mu       sync.RWMutex
	handlers map[uint32]wm.HotkeyHandler
}

var registry = &hotkeyRegistry{handlers: make(map[uint32]wm.HotkeyHandler)}

func (r *hotkeyRegistry) add(id uint32, h wm.HotkeyHandler) {
	r.mu.Lock()
	r.handlers[id] = h
	r.mu.Unlock()
}

func (r *hotkeyRegistry) remove(id uint32) {
	r.mu.Lock()
	delete(r.handlers, id)
	r.mu.Unlock()
}

func (r *hotkeyRegistry) lookup(id uint32) wm.HotkeyHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handlers[id]
}

// gwimHotkeyDispatch is the cgo trampoline invoked by Carbon on the main
// thread whenever a registered hotkey fires. We hand the work off to a
// goroutine so the main run loop stays responsive — a slow handler must
// never block tray menu interactions.
//
//export gwimHotkeyDispatch
func gwimHotkeyDispatch(id C.uint) {
	if h := registry.lookup(uint32(id)); h != nil {
		go h()
	}
}
