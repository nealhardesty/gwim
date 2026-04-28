//go:build darwin

package macos

/*
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <stdbool.h>

// gwim_frontmost_bundle_id returns a strdup'd C string for the frontmost
// application's bundle identifier, or NULL if unknown. Caller frees with
// free(). Some apps (e.g. helper tools) may have nil bundleIdentifier — in
// that case we fall back to localizedName so the suspension blocklist can
// still match.
static char *gwim_frontmost_bundle_id(void) {
    NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
    if (app == nil) return NULL;
    NSString *ident = [app bundleIdentifier];
    if (ident == nil) ident = [app localizedName];
    if (ident == nil) return NULL;
    const char *utf = [ident UTF8String];
    if (utf == NULL) return NULL;
    return strdup(utf);
}

// gwim_primary_screen_height returns the height of the primary screen
// (NSScreen.screens[0]). Used for NSScreen->AX coordinate conversion.
static double gwim_primary_screen_height(void) {
    NSArray<NSScreen *> *screens = [NSScreen screens];
    if (screens.count == 0) return 0;
    return [(NSScreen *)screens[0] frame].size.height;
}

// gwim_screen_count returns the number of attached screens.
static int gwim_screen_count(void) {
    return (int)[[NSScreen screens] count];
}

// gwim_screen_visible_frame fills the output pointers with the visibleFrame
// of NSScreen.screens[index] in NSScreen coordinates (origin = bottom-left
// of the primary display). Returns true on success.
static bool gwim_screen_visible_frame(int index, double *x, double *y, double *w, double *h) {
    NSArray<NSScreen *> *screens = [NSScreen screens];
    if (index < 0 || (NSUInteger)index >= screens.count) return false;
    NSRect r = [(NSScreen *)screens[index] visibleFrame];
    *x = r.origin.x; *y = r.origin.y; *w = r.size.width; *h = r.size.height;
    return true;
}

// gwim_screen_full_frame returns the FULL frame (including menu bar / dock)
// for screen[index]. Used when computing screen-containment for a window
// position because windows may sit under the menu bar.
static bool gwim_screen_full_frame(int index, double *x, double *y, double *w, double *h) {
    NSArray<NSScreen *> *screens = [NSScreen screens];
    if (index < 0 || (NSUInteger)index >= screens.count) return false;
    NSRect r = [(NSScreen *)screens[index] frame];
    *x = r.origin.x; *y = r.origin.y; *w = r.size.width; *h = r.size.height;
    return true;
}
*/
import "C"

import (
	"unsafe"

	"github.com/nealhardesty/gwim/internal/wm"
)

// macWindowManager is the macOS implementation of wm.WindowManager.
//
// It is stateless beyond cached cgo handles — every call queries the live
// system state, ensuring multi-monitor topology changes are picked up
// immediately without notification subscriptions.
type macWindowManager struct{}

// NewWindowManager constructs a macOS-backed WindowManager.
func NewWindowManager() wm.WindowManager {
	return &macWindowManager{}
}

// GetActiveWindow returns the currently focused window across all apps.
//
// Delegates to focusedWindow() (defined in window.go) because the underlying
// C function lives in window.go's cgo preamble and can't be called from
// this file's translation unit.
func (m *macWindowManager) GetActiveWindow() (wm.Window, error) {
	return focusedWindow()
}

// GetActiveAppIdentifier returns the bundle identifier of the foreground
// application, or its localized name as a fallback.
func (m *macWindowManager) GetActiveAppIdentifier() (string, error) {
	cstr := C.gwim_frontmost_bundle_id()
	if cstr == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr), nil
}

// GetScreenFrame returns the visible frame (in AX coordinates) of the
// screen that contains the supplied window.
func (m *macWindowManager) GetScreenFrame(w wm.Window) (wm.Rect, error) {
	frame, err := w.GetFrame()
	if err != nil {
		return wm.Rect{}, err
	}
	idx, err := screenIndexForRect(frame)
	if err != nil {
		return wm.Rect{}, err
	}
	return visibleFrameAX(idx)
}

// MoveWindowToScreen translocates the window to the screen adjacent in the
// given direction, scaling the frame proportionally so it occupies the same
// fractional area on the new screen. If no neighbour exists, the call is a
// no-op (no error).
func (m *macWindowManager) MoveWindowToScreen(w wm.Window, dir wm.ScreenDirection) error {
	frame, err := w.GetFrame()
	if err != nil {
		return err
	}
	srcIdx, err := screenIndexForRect(frame)
	if err != nil {
		return err
	}
	srcVF, err := visibleFrameAX(srcIdx)
	if err != nil {
		return err
	}
	dstIdx, ok := neighborScreen(srcIdx, dir)
	if !ok {
		return nil
	}
	dstVF, err := visibleFrameAX(dstIdx)
	if err != nil {
		return err
	}
	scaled := scaleRectBetweenScreens(frame, srcVF, dstVF)
	return w.SetFrame(scaled)
}

// scaleRectBetweenScreens maps a window rect from one screen's visible
// frame to another, preserving its relative position and size. This means
// a window occupying the top-right quarter of monitor A keeps that role on
// monitor B regardless of resolution differences.
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

// visibleFrameAX returns NSScreen.screens[idx].visibleFrame translated into
// AX (top-left-origin) coordinates.
func visibleFrameAX(idx int) (wm.Rect, error) {
	var x, y, w, h C.double
	if !bool(C.gwim_screen_visible_frame(C.int(idx), &x, &y, &w, &h)) {
		return wm.Rect{}, wm.ErrAccessibilityDenied
	}
	primaryH := float64(C.gwim_primary_screen_height())
	axY := primaryH - float64(y) - float64(h)
	return wm.Rect{X: float64(x), Y: axY, W: float64(w), H: float64(h)}, nil
}

// fullFrameAX returns NSScreen.screens[idx].frame in AX coordinates.
func fullFrameAX(idx int) (wm.Rect, error) {
	var x, y, w, h C.double
	if !bool(C.gwim_screen_full_frame(C.int(idx), &x, &y, &w, &h)) {
		return wm.Rect{}, wm.ErrAccessibilityDenied
	}
	primaryH := float64(C.gwim_primary_screen_height())
	axY := primaryH - float64(y) - float64(h)
	return wm.Rect{X: float64(x), Y: axY, W: float64(w), H: float64(h)}, nil
}

// screenIndexForRect picks the screen whose full frame contains the centre
// of the supplied rect (in AX coordinates). Falls back to the screen with
// the largest area of overlap if no screen contains the centre point.
func screenIndexForRect(r wm.Rect) (int, error) {
	count := int(C.gwim_screen_count())
	if count == 0 {
		return 0, wm.ErrAccessibilityDenied
	}
	cx := r.X + r.W/2
	cy := r.Y + r.H/2

	bestIdx := 0
	bestOverlap := -1.0
	for i := 0; i < count; i++ {
		f, err := fullFrameAX(i)
		if err != nil {
			continue
		}
		if cx >= f.X && cx <= f.X+f.W && cy >= f.Y && cy <= f.Y+f.H {
			return i, nil
		}
		ov := overlapArea(r, f)
		if ov > bestOverlap {
			bestOverlap = ov
			bestIdx = i
		}
	}
	return bestIdx, nil
}

// overlapArea returns the area of the intersection between two rects, or 0
// if they don't overlap.
func overlapArea(a, b wm.Rect) float64 {
	x1 := max(a.X, b.X)
	y1 := max(a.Y, b.Y)
	x2 := min(a.X+a.W, b.X+b.W)
	y2 := min(a.Y+a.H, b.Y+b.H)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	return (x2 - x1) * (y2 - y1)
}

// neighborScreen returns the index of the screen adjacent to src in the
// given direction. Adjacency is determined by full-frame centroids:
// among screens with centroids strictly past src's centroid in the given
// axis, pick the one with the smallest perpendicular distance.
func neighborScreen(srcIdx int, dir wm.ScreenDirection) (int, bool) {
	count := int(C.gwim_screen_count())
	if count <= 1 {
		return 0, false
	}
	src, err := fullFrameAX(srcIdx)
	if err != nil {
		return 0, false
	}
	srcCx := src.X + src.W/2
	srcCy := src.Y + src.H/2

	bestIdx := -1
	bestDist := 0.0
	for i := 0; i < count; i++ {
		if i == srcIdx {
			continue
		}
		f, err := fullFrameAX(i)
		if err != nil {
			continue
		}
		cx := f.X + f.W/2
		cy := f.Y + f.H/2

		var primary, perp float64
		switch dir {
		case wm.ScreenWest:
			if cx >= srcCx {
				continue
			}
			primary = srcCx - cx
			perp = abs(cy - srcCy)
		case wm.ScreenEast:
			if cx <= srcCx {
				continue
			}
			primary = cx - srcCx
			perp = abs(cy - srcCy)
		case wm.ScreenNorth:
			if cy >= srcCy {
				continue
			}
			primary = srcCy - cy
			perp = abs(cx - srcCx)
		case wm.ScreenSouth:
			if cy <= srcCy {
				continue
			}
			primary = cy - srcCy
			perp = abs(cx - srcCx)
		default:
			continue
		}
		score := primary + perp*0.25
		if bestIdx == -1 || score < bestDist {
			bestIdx = i
			bestDist = score
		}
	}
	if bestIdx == -1 {
		return 0, false
	}
	return bestIdx, true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
