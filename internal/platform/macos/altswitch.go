//go:build darwin

package macos

/*
#cgo darwin LDFLAGS: -framework Cocoa -framework AppKit -framework ApplicationServices -framework CoreGraphics -framework Carbon
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include <sys/types.h>

typedef struct {
    pid_t    pid;
    uint32_t cgid;
    char    *title;
    char    *app_name;
} gwim_window_entry;

extern int  gwim_enumerate_windows(gwim_window_entry *out_arr, int max,
                                    pid_t *out_focused_pid,
                                    uint32_t *out_focused_cgid);
extern void gwim_free_window_entries(gwim_window_entry *arr, int count);
extern bool gwim_raise_window(pid_t pid, uint32_t cgid);

extern void gwim_overlay_show(int *pids, const char **titles_and_apps,
                               int count, int selected);
extern void gwim_overlay_update_selected(int idx);
extern void gwim_overlay_hide(void);

extern bool gwim_eventtap_install(void);
extern void gwim_eventtap_remove(void);
extern bool gwim_option_currently_down(void);
*/
import "C"

import (
	"log"
	"sync"
	"unsafe"

	"github.com/nealhardesty/gwim/internal/altswitch"
	"github.com/nealhardesty/gwim/internal/wm"
)

// macOS virtual keycodes referenced by the event-tap dispatcher.
const (
	mkVKReturn = 36
	mkVKTab    = 48
	mkVKEsc    = 53
	mkVKEnter  = 76
)

// switcher implements wm.Switcher. It owns the MRU stash, the open/closed
// state, and the parallel slices of (display item, MRU key) for the
// currently-shown overlay. The native overlay window and event tap are
// managed via the C functions in altswitch_native.m.
//
// Concurrency: open/closed transitions and selected-index mutations all
// take s.mu. The native event tap callback runs on the main thread and
// invokes Go via gwimAltswitchEvent, which routes through the package-
// level "active switcher" pointer (only one switcher is open at a time).
type switcher struct {
	stash *altswitch.Stash

	mu       sync.Mutex
	open     bool
	holdMode bool // true ⇒ Option release commits (hotkey-style)
	items    []wm.WindowInfo
	keys     []altswitch.Key
	selected int
}

// NewSwitcher constructs the macOS Alt-Tab controller.
//
// The wmgr argument is currently unused (enumeration goes through cgo
// directly) but accepted for symmetry with the rest of the platform
// constructors and to leave room for sharing with other features later.
func NewSwitcher(_ wm.WindowManager) wm.Switcher {
	return &switcher{stash: altswitch.NewStash(64)}
}

// OpenForward / OpenBackward are the two entry points wired to the
// Carbon hotkey table (Option+Tab and Option+Shift+Tab) and to the
// equivalent tray-menu items.
func (s *switcher) OpenForward()  { s.openOrAdvance(true) }
func (s *switcher) OpenBackward() { s.openOrAdvance(false) }

func (s *switcher) openOrAdvance(forward bool) {
	s.mu.Lock()
	if s.open {
		s.advanceLocked(forward)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	items, keys, focused := enumerateWindows()
	if len(items) == 0 {
		return
	}

	perm := s.stash.Order(keys, focused)
	orderedItems := make([]wm.WindowInfo, len(perm))
	orderedKeys := make([]altswitch.Key, len(perm))
	for i, p := range perm {
		orderedItems[i] = items[p]
		orderedKeys[i] = keys[p]
	}

	sel := 0
	if len(orderedItems) >= 2 {
		if forward {
			sel = 1
		} else {
			sel = len(orderedItems) - 1
		}
	}

	holdMode := bool(C.gwim_option_currently_down())

	s.mu.Lock()
	s.items = orderedItems
	s.keys = orderedKeys
	s.selected = sel
	s.holdMode = holdMode
	s.open = true
	s.mu.Unlock()

	setActive(s)
	showOverlay(orderedItems, sel)

	if !installEventTap() {
		log.Printf("altswitch: failed to install event tap (Accessibility denied?)")
		// Drop everything; the overlay alone can't drive the switcher.
		go s.cancel()
	}
}

// advanceLocked mutates the highlight under s.mu and dispatches the UI
// update on a goroutine (the overlay update is async by design).
func (s *switcher) advanceLocked(forward bool) {
	if len(s.items) == 0 {
		return
	}
	n := len(s.items)
	if forward {
		s.selected = (s.selected + 1) % n
	} else {
		s.selected = (s.selected - 1 + n) % n
	}
	sel := s.selected
	go updateOverlaySelected(sel)
}

// commit closes the overlay and raises the highlighted window.
func (s *switcher) commit() {
	s.mu.Lock()
	if !s.open {
		s.mu.Unlock()
		return
	}
	s.open = false
	items := s.items
	keys := s.keys
	sel := s.selected
	s.items = nil
	s.keys = nil
	s.mu.Unlock()

	clearActive(s)
	removeEventTap()
	hideOverlay()

	if sel < 0 || sel >= len(items) {
		return
	}
	s.stash.Promote(keys[sel])
	if !raiseWindow(items[sel].PID, items[sel].CGID) {
		log.Printf("altswitch: raise failed pid=%d cgid=%d title=%q",
			items[sel].PID, items[sel].CGID, items[sel].Title)
	}
}

// cancel closes the overlay without raising anything.
func (s *switcher) cancel() {
	s.mu.Lock()
	if !s.open {
		s.mu.Unlock()
		return
	}
	s.open = false
	s.items = nil
	s.keys = nil
	s.mu.Unlock()

	clearActive(s)
	removeEventTap()
	hideOverlay()
}

// =====================================================================
// cgo helpers (all calls into altswitch_native.m).
// =====================================================================

func enumerateWindows() ([]wm.WindowInfo, []altswitch.Key, altswitch.Key) {
	const maxN = 256
	arr := (*C.gwim_window_entry)(C.calloc(maxN, C.size_t(unsafe.Sizeof(C.gwim_window_entry{}))))
	if arr == nil {
		return nil, nil, altswitch.Key{}
	}
	defer C.free(unsafe.Pointer(arr))

	var fpid C.pid_t
	var fcgid C.uint32_t
	n := int(C.gwim_enumerate_windows(arr, C.int(maxN), &fpid, &fcgid))
	defer C.gwim_free_window_entries(arr, C.int(n))

	if n <= 0 {
		return nil, nil, altswitch.Key{}
	}

	items := make([]wm.WindowInfo, n)
	keys := make([]altswitch.Key, n)
	stride := unsafe.Sizeof(C.gwim_window_entry{})
	for i := 0; i < n; i++ {
		e := (*C.gwim_window_entry)(unsafe.Add(unsafe.Pointer(arr), uintptr(i)*stride))
		info := wm.WindowInfo{
			PID:  int32(e.pid),
			CGID: uint32(e.cgid),
		}
		if e.title != nil {
			info.Title = C.GoString(e.title)
		}
		if e.app_name != nil {
			info.AppName = C.GoString(e.app_name)
		}
		items[i] = info
		keys[i] = altswitch.Key{PID: info.PID, CGID: info.CGID}
	}
	return items, keys, altswitch.Key{PID: int32(fpid), CGID: uint32(fcgid)}
}

func showOverlay(items []wm.WindowInfo, selected int) {
	n := len(items)
	if n == 0 {
		return
	}
	pids := make([]C.int, n)
	titlePtrs := make([]*C.char, n*2)
	allocs := make([]*C.char, 0, n*2)
	defer func() {
		for _, p := range allocs {
			C.free(unsafe.Pointer(p))
		}
	}()

	for i, it := range items {
		pids[i] = C.int(it.PID)
		ct := C.CString(it.Title)
		ca := C.CString(it.AppName)
		allocs = append(allocs, ct, ca)
		titlePtrs[i*2] = ct
		titlePtrs[i*2+1] = ca
	}

	C.gwim_overlay_show(
		(*C.int)(unsafe.Pointer(&pids[0])),
		(**C.char)(unsafe.Pointer(&titlePtrs[0])),
		C.int(n),
		C.int(selected),
	)
}

func updateOverlaySelected(idx int) { C.gwim_overlay_update_selected(C.int(idx)) }
func hideOverlay()                  { C.gwim_overlay_hide() }
func installEventTap() bool         { return bool(C.gwim_eventtap_install()) }
func removeEventTap()               { C.gwim_eventtap_remove() }

func raiseWindow(pid int32, cgid uint32) bool {
	return bool(C.gwim_raise_window(C.pid_t(pid), C.uint32_t(cgid)))
}

// =====================================================================
// Active-switcher singleton.
//
// Only one overlay is ever open at a time, so a single package-level
// pointer is enough to route event-tap callbacks back to the right Go
// instance. The mutex protects the pointer itself, not the switcher.
// =====================================================================

var (
	activeMu       sync.Mutex
	activeInstance *switcher
)

func setActive(s *switcher) { activeMu.Lock(); activeInstance = s; activeMu.Unlock() }
func clearActive(s *switcher) {
	activeMu.Lock()
	if activeInstance == s {
		activeInstance = nil
	}
	activeMu.Unlock()
}
func getActive() *switcher {
	activeMu.Lock()
	defer activeMu.Unlock()
	return activeInstance
}

// gwimAltswitchEvent is invoked by the C event-tap callback in
// altswitch_native.m. It runs on the main thread; we keep this function
// cheap (just lock/unlock and dispatch goroutines) so the run loop stays
// responsive.
//
// kind values are documented in altswitch_native.m:
//
//	0 = key down  (keycode + modifier flags)
//	1 = flags changed (optionDown only — we ignore the rest)
//
//export gwimAltswitchEvent
func gwimAltswitchEvent(kind, keycode, optionDown, shiftDown C.int) {
	s := getActive()
	if s == nil {
		return
	}
	switch kind {
	case 0: // key down
		switch keycode {
		case mkVKTab:
			s.mu.Lock()
			s.advanceLocked(shiftDown == 0)
			s.mu.Unlock()
		case mkVKEsc:
			go s.cancel()
		case mkVKReturn, mkVKEnter:
			go s.commit()
		}
	case 1: // flags changed
		// In hotkey-style invocations the user is holding Option when the
		// switcher opens; releasing it commits. Tray-click invocations
		// have holdMode=false, so a stray Option toggle does NOT commit.
		s.mu.Lock()
		hold := s.holdMode
		s.mu.Unlock()
		if hold && optionDown == 0 {
			go s.commit()
		}
	}
}
