//go:build windows

package windows

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/nealhardesty/gwim/internal/wm"
)

// Win32 binding surface.
//
// We bind via syscall.LazyDLL / LazyProc instead of cgo because this
// package is pure Go — that keeps cross-compilation from a non-Windows
// dev machine simple and matches the "minimal external dependencies"
// directive in GOLANG.md.
//
// The macOS platform package uses cgo only because it needs to call into
// Objective-C and Carbon (no equivalent option exists). On Windows, every
// API we need lives in user32.dll / kernel32.dll and is reachable through
// the standard syscall package.
var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsZoomed            = user32.NewProc("IsZoomed")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
)

// Win32 constants we reference. Keeping them local avoids a hard
// dependency on the version of golang.org/x/sys that exposes them.
const (
	swpNoZorder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040

	swRestore  = 9
	swMaximize = 3

	gwlStyle   = -16
	gwlExStyle = -20

	wsOverlapped       = 0x00000000
	wsCaption          = 0x00C00000
	wsThickFrame       = 0x00040000
	wsMinimizeBox      = 0x00020000
	wsMaximizeBox      = 0x00010000
	wsSysMenu          = 0x00080000
	wsOverlappedWindow = wsCaption | wsThickFrame | wsMinimizeBox | wsMaximizeBox | wsSysMenu
	wsExClientEdge     = 0x00000200
	wsExWindowEdge     = 0x00000100

	monitorDefaultToNearest = 0x00000002
)

// rect mirrors the Win32 RECT struct (LPRECT). All four fields are 32-bit
// signed coordinates measured in physical pixels relative to the virtual
// screen origin (top-left of the primary monitor).
type rect struct {
	Left, Top, Right, Bottom int32
}

// monitorInfo mirrors the Win32 MONITORINFO struct. CbSize MUST be set to
// sizeof(monitorInfo) before calling GetMonitorInfoW.
type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

// =====================================================================
// wm.Window — single-window manipulation
// =====================================================================

// winWindow is the Windows implementation of wm.Window. It wraps a raw
// HWND handle.
//
// The engine creates a fresh winWindow per GetActiveWindow() call so the
// hwnd value is always live at the moment of use; we never cache HWNDs
// across calls. State that must persist across GetActiveWindow boundaries
// (e.g. the borderless-fullscreen restore stash) lives in package-level
// maps keyed by hwnd, see fullscreenStash.
type winWindow struct {
	hwnd uintptr
}

// GetFrame returns the window's outer frame in screen pixels.
func (w *winWindow) GetFrame() (wm.Rect, error) {
	var r rect
	ret, _, _ := procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return wm.Rect{}, wm.ErrNoActiveWindow
	}
	return wm.Rect{
		X: float64(r.Left),
		Y: float64(r.Top),
		W: float64(r.Right - r.Left),
		H: float64(r.Bottom - r.Top),
	}, nil
}

// SetFrame moves and resizes the window. We unmaximize first so a snap
// onto a maximized window doesn't no-op (Windows ignores SetWindowPos
// position changes while WS_MAXIMIZE is set).
func (w *winWindow) SetFrame(r wm.Rect) error {
	if isZoomed(w.hwnd) {
		showWindow(w.hwnd, swRestore)
	}
	x := int32(r.X)
	y := int32(r.Y)
	cx := int32(r.W)
	cy := int32(r.H)
	if cx < 1 {
		cx = 1
	}
	if cy < 1 {
		cy = 1
	}
	ret, _, _ := procSetWindowPos.Call(
		w.hwnd,
		0, // hwndInsertAfter
		uintptr(x),
		uintptr(y),
		uintptr(cx),
		uintptr(cy),
		uintptr(swpNoZorder|swpNoActivate),
	)
	if ret == 0 {
		return wm.ErrNoActiveWindow
	}
	return nil
}

// ToggleFullScreen toggles borderless-fullscreen on the active window
// (per the user's choice — Windows lacks a true "Spaces" fullscreen
// equivalent, and SW_MAXIMIZE leaves the title bar visible which doesn't
// match the expectation of Ctrl+Alt+F).
//
// First call: stash original style + frame, strip WS_OVERLAPPEDWINDOW,
// resize to the containing monitor's full frame.
// Second call: restore the stashed style + frame, drop the stash entry.
func (w *winWindow) ToggleFullScreen() error {
	if entry, ok := fullscreenStashGet(w.hwnd); ok {
		// Restore.
		setWindowLong(w.hwnd, gwlStyle, entry.style)
		setWindowLong(w.hwnd, gwlExStyle, entry.exStyle)
		ret, _, _ := procSetWindowPos.Call(
			w.hwnd, 0,
			uintptr(entry.frame.Left),
			uintptr(entry.frame.Top),
			uintptr(entry.frame.Right-entry.frame.Left),
			uintptr(entry.frame.Bottom-entry.frame.Top),
			uintptr(swpNoZorder|swpNoActivate|swpFrameChanged|swpShowWindow),
		)
		fullscreenStashDel(w.hwnd)
		if ret == 0 {
			return wm.ErrNoActiveWindow
		}
		return nil
	}

	// Engage. Capture state before mutating.
	var current rect
	if r, _, _ := procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&current))); r == 0 {
		return wm.ErrNoActiveWindow
	}
	style := getWindowLong(w.hwnd, gwlStyle)
	exStyle := getWindowLong(w.hwnd, gwlExStyle)

	mon, err := monitorFullFrame(w.hwnd)
	if err != nil {
		return err
	}

	fullscreenStashSet(w.hwnd, fullscreenEntry{
		style:   style,
		exStyle: exStyle,
		frame:   current,
	})

	// Strip window chrome. We keep WS_VISIBLE etc. by ANDing only the
	// chrome bits we want to remove.
	newStyle := style &^ uintptr(wsOverlappedWindow)
	newExStyle := exStyle &^ uintptr(wsExClientEdge|wsExWindowEdge)
	setWindowLong(w.hwnd, gwlStyle, newStyle)
	setWindowLong(w.hwnd, gwlExStyle, newExStyle)

	ret, _, _ := procSetWindowPos.Call(
		w.hwnd, 0,
		uintptr(mon.Left),
		uintptr(mon.Top),
		uintptr(mon.Right-mon.Left),
		uintptr(mon.Bottom-mon.Top),
		uintptr(swpNoZorder|swpFrameChanged|swpShowWindow),
	)
	if ret == 0 {
		return wm.ErrNoActiveWindow
	}
	return nil
}

// =====================================================================
// Borderless-fullscreen stash
//
// The engine builds a fresh winWindow per GetActiveWindow() call, so the
// "are we currently fullscreen?" flag must live somewhere the next call
// can find it. We keep a process-wide map keyed by HWND. Values are
// removed on toggle-off; on app exit the map simply leaks (acceptable —
// it's bounded by the number of windows the user has fullscreened).
// =====================================================================

type fullscreenEntry struct {
	style   uintptr
	exStyle uintptr
	frame   rect
}

var (
	fullscreenMu sync.Mutex
	fullscreen   = map[uintptr]fullscreenEntry{}
)

func fullscreenStashGet(h uintptr) (fullscreenEntry, bool) {
	fullscreenMu.Lock()
	defer fullscreenMu.Unlock()
	e, ok := fullscreen[h]
	return e, ok
}

func fullscreenStashSet(h uintptr, e fullscreenEntry) {
	fullscreenMu.Lock()
	fullscreen[h] = e
	fullscreenMu.Unlock()
}

func fullscreenStashDel(h uintptr) {
	fullscreenMu.Lock()
	delete(fullscreen, h)
	fullscreenMu.Unlock()
}

// =====================================================================
// wm.WindowManager — discovery + screen geometry
// =====================================================================

// winWindowManager is the Windows implementation of wm.WindowManager.
//
// It is stateless beyond the live HWND each call resolves; multi-monitor
// topology is queried fresh on every screen-related call so plugging /
// unplugging displays takes effect immediately without notification
// subscriptions (matching the macOS implementation).
type winWindowManager struct{}

// NewWindowManager constructs a Windows-backed WindowManager.
func NewWindowManager() wm.WindowManager {
	return &winWindowManager{}
}

// GetActiveWindow returns the currently focused top-level window.
func (m *winWindowManager) GetActiveWindow() (wm.Window, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil, wm.ErrNoActiveWindow
	}
	return &winWindow{hwnd: hwnd}, nil
}

// GetScreenFrame returns the work area (excluding the taskbar) of the
// monitor that contains the supplied window — the Windows analogue of
// macOS NSScreen.visibleFrame.
func (m *winWindowManager) GetScreenFrame(w wm.Window) (wm.Rect, error) {
	ww, ok := w.(*winWindow)
	if !ok {
		return wm.Rect{}, wm.ErrNoActiveWindow
	}
	work, err := monitorWorkFrame(ww.hwnd)
	if err != nil {
		return wm.Rect{}, err
	}
	return rectToWM(work), nil
}

// MoveWindowToScreen translocates the window to the screen adjacent in
// the given direction, scaling the frame proportionally so it occupies
// the same fractional area on the new screen.
//
// The geometry math is shared with macOS — the algorithm only needs
// top-left-origin rects, which is exactly what Windows already uses.
func (m *winWindowManager) MoveWindowToScreen(w wm.Window, dir wm.ScreenDirection) error {
	ww, ok := w.(*winWindow)
	if !ok {
		return wm.ErrNoActiveWindow
	}

	frame, err := w.GetFrame()
	if err != nil {
		return err
	}

	monitors := enumMonitors()
	if len(monitors) == 0 {
		return wm.ErrNoActiveWindow
	}

	srcIdx := screenIndexForRect(frame, monitors)
	if srcIdx < 0 {
		srcIdx = 0
	}
	dstIdx, ok := neighborScreen(srcIdx, dir, monitors)
	if !ok {
		return nil
	}

	srcWork := rectToWM(monitors[srcIdx].work)
	dstWork := rectToWM(monitors[dstIdx].work)
	scaled := scaleRectBetweenScreens(frame, srcWork, dstWork)

	// Unmaximize on the source so SetWindowPos isn't ignored, then move.
	if isZoomed(ww.hwnd) {
		showWindow(ww.hwnd, swRestore)
	}
	return ww.SetFrame(scaled)
}

// =====================================================================
// Monitor enumeration helpers
// =====================================================================

// monInfo is a snapshot of one monitor's full frame and work area. We
// take a snapshot per call rather than watching for display change
// notifications — multi-monitor topology rarely changes often enough to
// justify the bookkeeping, and per-call enumeration always reflects
// reality.
type monInfo struct {
	full rect
	work rect
}

// EnumDisplayMonitors callback dispatch.
//
// The Windows callback receives a LPARAM we control; we use it as a
// monotonic ID into a sync map of result accumulators rather than a
// slice index, so concurrent enumerations from different goroutines
// don't trample each other (unlikely but cheap to support).
//
// The callback itself is created once at package init — syscall.NewCallback
// allocates per call and the docs explicitly recommend reusing the result.
var (
	enumMu      sync.Mutex
	enumNextID  uintptr
	enumPending = map[uintptr]*[]monInfo{}
	enumCBOnce  sync.Once
	enumCB      uintptr
)

// enumMonitors returns one monInfo entry per attached display in the
// order Windows reports them.
func enumMonitors() []monInfo {
	enumCBOnce.Do(func() {
		enumCB = syscall.NewCallback(enumMonitorsProc)
	})

	out := make([]monInfo, 0, 4)
	enumMu.Lock()
	enumNextID++
	id := enumNextID
	enumPending[id] = &out
	enumMu.Unlock()
	defer func() {
		enumMu.Lock()
		delete(enumPending, id)
		enumMu.Unlock()
	}()

	procEnumDisplayMonitors.Call(0, 0, enumCB, id)
	return out
}

// enumMonitorsProc is the EnumDisplayMonitors callback. Windows runs it
// synchronously on the calling thread; the LPARAM is the dispatch ID we
// allocated in enumMonitors.
func enumMonitorsProc(hMonitor, _ uintptr, lprcMonitor uintptr, lParam uintptr) uintptr {
	enumMu.Lock()
	target := enumPending[lParam]
	enumMu.Unlock()
	if target == nil {
		return 1 // continue enumerating other monitors
	}

	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return 1
	}
	enumMu.Lock()
	*target = append(*target, monInfo{full: mi.RcMonitor, work: mi.RcWork})
	enumMu.Unlock()
	return 1
}

// monitorWorkFrame returns the work area (excluding taskbar) of the
// monitor containing hwnd.
func monitorWorkFrame(hwnd uintptr) (rect, error) {
	hMon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	if hMon == 0 {
		return rect{}, wm.ErrNoActiveWindow
	}
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rect{}, wm.ErrNoActiveWindow
	}
	return mi.RcWork, nil
}

// monitorFullFrame mirrors monitorWorkFrame but returns the full monitor
// frame (including taskbar). Used by ToggleFullScreen so borderless
// fullscreen really fills the screen.
func monitorFullFrame(hwnd uintptr) (rect, error) {
	hMon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	if hMon == 0 {
		return rect{}, wm.ErrNoActiveWindow
	}
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rect{}, wm.ErrNoActiveWindow
	}
	return mi.RcMonitor, nil
}

// =====================================================================
// Geometry helpers (ported from internal/platform/macos/workspace.go).
// The math is platform-agnostic — both AX (macOS) and Win32 use top-
// left-origin rectangles, so the same algorithm works without flipping.
// =====================================================================

// rectToWM converts a Win32 RECT to wm.Rect (float64 origin+size).
func rectToWM(r rect) wm.Rect {
	return wm.Rect{
		X: float64(r.Left),
		Y: float64(r.Top),
		W: float64(r.Right - r.Left),
		H: float64(r.Bottom - r.Top),
	}
}

// screenIndexForRect picks the monitor whose full frame contains the
// centre of r. Falls back to the monitor with the largest area of overlap
// when no centre-hit is found.
func screenIndexForRect(r wm.Rect, monitors []monInfo) int {
	if len(monitors) == 0 {
		return -1
	}
	cx := r.X + r.W/2
	cy := r.Y + r.H/2

	bestIdx := 0
	bestOverlap := -1.0
	for i, m := range monitors {
		f := rectToWM(m.full)
		if cx >= f.X && cx <= f.X+f.W && cy >= f.Y && cy <= f.Y+f.H {
			return i
		}
		ov := overlapArea(r, f)
		if ov > bestOverlap {
			bestOverlap = ov
			bestIdx = i
		}
	}
	return bestIdx
}

// neighborScreen returns the index of the monitor adjacent to src in the
// given direction, using the same centroid-distance heuristic as the
// macOS implementation.
func neighborScreen(srcIdx int, dir wm.ScreenDirection, monitors []monInfo) (int, bool) {
	if len(monitors) <= 1 {
		return 0, false
	}
	src := rectToWM(monitors[srcIdx].full)
	srcCx := src.X + src.W/2
	srcCy := src.Y + src.H/2

	bestIdx := -1
	bestScore := 0.0
	for i, m := range monitors {
		if i == srcIdx {
			continue
		}
		f := rectToWM(m.full)
		cx := f.X + f.W/2
		cy := f.Y + f.H/2

		var primary, perp float64
		switch dir {
		case wm.ScreenWest:
			if cx >= srcCx {
				continue
			}
			primary = srcCx - cx
			perp = absf(cy - srcCy)
		case wm.ScreenEast:
			if cx <= srcCx {
				continue
			}
			primary = cx - srcCx
			perp = absf(cy - srcCy)
		case wm.ScreenNorth:
			if cy >= srcCy {
				continue
			}
			primary = srcCy - cy
			perp = absf(cx - srcCx)
		case wm.ScreenSouth:
			if cy <= srcCy {
				continue
			}
			primary = cy - srcCy
			perp = absf(cx - srcCx)
		default:
			continue
		}
		score := primary + perp*0.25
		if bestIdx == -1 || score < bestScore {
			bestIdx = i
			bestScore = score
		}
	}
	if bestIdx == -1 {
		return 0, false
	}
	return bestIdx, true
}

// scaleRectBetweenScreens maps a window rect from one screen's work
// area to another, preserving its relative position and size. Identical
// to the macOS implementation by design — same math, no coordinate flip
// because both platforms use top-left-origin rectangles.
func scaleRectBetweenScreens(win, src, dst wm.Rect) wm.Rect {
	if src.W == 0 || src.H == 0 {
		return wm.Rect{X: dst.X, Y: dst.Y, W: dst.W / 2, H: dst.H / 2}
	}
	relX := (win.X - src.X) / src.W
	relY := (win.Y - src.Y) / src.H
	relW := win.W / src.W
	relH := win.H / src.H
	return wm.Rect{
		X: dst.X + relX*dst.W,
		Y: dst.Y + relY*dst.H,
		W: relW * dst.W,
		H: relH * dst.H,
	}
}

// overlapArea returns the intersection area of two rects, 0 if disjoint.
func overlapArea(a, b wm.Rect) float64 {
	x1 := maxf(a.X, b.X)
	y1 := maxf(a.Y, b.Y)
	x2 := minf(a.X+a.W, b.X+b.W)
	y2 := minf(a.Y+a.H, b.Y+b.H)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	return (x2 - x1) * (y2 - y1)
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// =====================================================================
// Win32 helper wrappers
// =====================================================================

func isZoomed(hwnd uintptr) bool {
	ret, _, _ := procIsZoomed.Call(hwnd)
	return ret != 0
}

// showWindow returns true on success but most callers ignore the result.
// Wrapping the proc keeps the call sites readable.
func showWindow(hwnd uintptr, cmd int32) bool {
	ret, _, _ := procShowWindow.Call(hwnd, uintptr(cmd))
	return ret != 0
}

func getWindowLong(hwnd uintptr, index int32) uintptr {
	ret, _, _ := procGetWindowLongPtrW.Call(hwnd, uintptr(int32(index)))
	return ret
}

func setWindowLong(hwnd uintptr, index int32, value uintptr) uintptr {
	ret, _, _ := procSetWindowLongPtrW.Call(hwnd, uintptr(int32(index)), value)
	return ret
}
