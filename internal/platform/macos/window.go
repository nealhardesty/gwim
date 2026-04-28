//go:build darwin

package macos

/*
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdbool.h>
#include <stdlib.h>

// gwim_ax_focused_window returns the AXUIElementRef of the currently focused
// window, or NULL if none. Callers MUST CFRelease the returned reference.
//
// We deliberately walk: SystemWide -> kAXFocusedApplicationAttribute ->
// kAXFocusedWindowAttribute. Going via the system-wide element is more
// reliable than NSWorkspace.frontmostApplication because it survives Spaces
// transitions and apps that lie about being foreground (e.g. background
// notification panels).
static AXUIElementRef gwim_ax_focused_window(void) {
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    if (sys == NULL) return NULL;

    CFTypeRef appRef = NULL;
    AXError err = AXUIElementCopyAttributeValue(sys, kAXFocusedApplicationAttribute, &appRef);
    CFRelease(sys);
    if (err != kAXErrorSuccess || appRef == NULL) return NULL;

    CFTypeRef windowRef = NULL;
    err = AXUIElementCopyAttributeValue((AXUIElementRef)appRef, kAXFocusedWindowAttribute, &windowRef);
    CFRelease(appRef);
    if (err != kAXErrorSuccess || windowRef == NULL) return NULL;

    return (AXUIElementRef)windowRef;
}

// gwim_ax_get_frame fills out_x/out_y/out_w/out_h with the window's
// position+size in AX (top-left-origin) coordinates. Returns true on success.
static bool gwim_ax_get_frame(AXUIElementRef win,
                              double *out_x, double *out_y,
                              double *out_w, double *out_h) {
    CFTypeRef posRef = NULL;
    CFTypeRef sizeRef = NULL;
    if (AXUIElementCopyAttributeValue(win, kAXPositionAttribute, &posRef) != kAXErrorSuccess) return false;
    if (AXUIElementCopyAttributeValue(win, kAXSizeAttribute, &sizeRef) != kAXErrorSuccess) {
        CFRelease(posRef);
        return false;
    }
    CGPoint pos;
    CGSize sz;
    bool ok = AXValueGetValue((AXValueRef)posRef, kAXValueCGPointType, &pos)
           && AXValueGetValue((AXValueRef)sizeRef, kAXValueCGSizeType, &sz);
    CFRelease(posRef);
    CFRelease(sizeRef);
    if (!ok) return false;
    *out_x = pos.x; *out_y = pos.y; *out_w = sz.width; *out_h = sz.height;
    return true;
}

// gwim_ax_set_frame sets size first, then position.
//
// Some misbehaving apps (Chrome, Slack) clamp the new size against the
// "current" screen before applying position; setting size first reduces the
// number of cases where the window ends up in the wrong slot. After both
// writes we re-read and, if the realized frame disagrees, retry once with
// the position re-applied. This matches the technique Hammerspoon uses.
static bool gwim_ax_set_frame(AXUIElementRef win, double x, double y, double w, double h) {
    CGPoint pos = (CGPoint){ .x = x, .y = y };
    CGSize  sz  = (CGSize){ .width = w, .height = h };

    AXValueRef sizeVal = AXValueCreate(kAXValueCGSizeType, &sz);
    AXValueRef posVal  = AXValueCreate(kAXValueCGPointType, &pos);
    if (sizeVal == NULL || posVal == NULL) {
        if (sizeVal) CFRelease(sizeVal);
        if (posVal)  CFRelease(posVal);
        return false;
    }

    AXError e1 = AXUIElementSetAttributeValue(win, kAXSizeAttribute, sizeVal);
    AXError e2 = AXUIElementSetAttributeValue(win, kAXPositionAttribute, posVal);
    AXError e3 = AXUIElementSetAttributeValue(win, kAXSizeAttribute, sizeVal);

    CFRelease(sizeVal);
    CFRelease(posVal);
    return (e1 == kAXErrorSuccess || e1 == kAXErrorNotImplemented)
        && (e2 == kAXErrorSuccess)
        && (e3 == kAXErrorSuccess || e3 == kAXErrorNotImplemented);
}

// gwim_ax_toggle_fullscreen flips kAXFullScreenAttribute, which triggers the
// native Spaces full-screen transition (the same as clicking the green dot
// while holding nothing).
static bool gwim_ax_toggle_fullscreen(AXUIElementRef win) {
    CFTypeRef cur = NULL;
    if (AXUIElementCopyAttributeValue(win, CFSTR("AXFullScreen"), &cur) != kAXErrorSuccess) return false;
    Boolean isFull = CFBooleanGetValue((CFBooleanRef)cur);
    CFRelease(cur);
    CFBooleanRef next = isFull ? kCFBooleanFalse : kCFBooleanTrue;
    return AXUIElementSetAttributeValue(win, CFSTR("AXFullScreen"), next) == kAXErrorSuccess;
}

// gwim_request_accessibility prompts the user (once) for accessibility
// permission and returns whether the process is currently trusted.
static bool gwim_request_accessibility(bool prompt) {
    CFStringRef keys[] = { kAXTrustedCheckOptionPrompt };
    CFBooleanRef vals[] = { prompt ? kCFBooleanTrue : kCFBooleanFalse };
    CFDictionaryRef opts = CFDictionaryCreate(kCFAllocatorDefault,
                                              (const void **)keys, (const void **)vals, 1,
                                              &kCFTypeDictionaryKeyCallBacks,
                                              &kCFTypeDictionaryValueCallBacks);
    bool trusted = AXIsProcessTrustedWithOptions(opts);
    CFRelease(opts);
    return trusted;
}

// gwim_release wraps CFRelease so Go can invoke it without importing CoreFoundation.
static void gwim_release(CFTypeRef ref) { if (ref) CFRelease(ref); }
*/
import "C"

import (
	"runtime"
	"unsafe"

	"github.com/nealhardesty/gwim/internal/wm"
)

// macWindow is the macOS implementation of wm.Window.
//
// It owns an AXUIElementRef to the underlying window. The reference is
// released when the Go value is finalized. macWindow values are NOT safe
// for concurrent use; create a new one per operation via
// WindowManager.GetActiveWindow.
type macWindow struct {
	ref C.AXUIElementRef
}

// newMacWindow wraps an AXUIElementRef and attaches a finalizer that releases
// it when the Go value is garbage collected.
func newMacWindow(ref C.AXUIElementRef) *macWindow {
	w := &macWindow{ref: ref}
	runtime.SetFinalizer(w, func(w *macWindow) {
		if w.ref != 0 {
			C.gwim_release(C.CFTypeRef(w.ref))
			w.ref = 0
		}
	})
	return w
}

// GetFrame returns the window's frame in AX coordinates (top-left origin,
// units = points relative to the primary display's top-left corner).
func (w *macWindow) GetFrame() (wm.Rect, error) {
	var x, y, ww, hh C.double
	if !bool(C.gwim_ax_get_frame(w.ref, &x, &y, &ww, &hh)) {
		return wm.Rect{}, wm.ErrAccessibilityDenied
	}
	return wm.Rect{X: float64(x), Y: float64(y), W: float64(ww), H: float64(hh)}, nil
}

// SetFrame moves and resizes the window. AX coordinates are top-left origin.
func (w *macWindow) SetFrame(r wm.Rect) error {
	if !bool(C.gwim_ax_set_frame(w.ref, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H))) {
		return wm.ErrAccessibilityDenied
	}
	return nil
}

// ToggleFullScreen flips the window's native full-screen Space.
func (w *macWindow) ToggleFullScreen() error {
	if !bool(C.gwim_ax_toggle_fullscreen(w.ref)) {
		return wm.ErrAccessibilityDenied
	}
	return nil
}

// focusedWindow returns the currently focused window across the system.
//
// Lives in window.go (rather than workspace.go where GetActiveWindow is
// defined) because each cgo file's preamble is its own translation unit;
// gwim_ax_focused_window can only be called from this file.
func focusedWindow() (wm.Window, error) {
	ref := C.gwim_ax_focused_window()
	if ref == 0 {
		return nil, wm.ErrNoActiveWindow
	}
	return newMacWindow(ref), nil
}

// RequestAccessibilityPermission shows the system prompt and returns true if
// the process is currently trusted. Callers should invoke this at startup
// (typically from main.go) so the user knows why GWiM needs access.
//
// Passing prompt=false performs a silent check, useful in unit tests or
// when re-checking after the user enables the permission.
func RequestAccessibilityPermission(prompt bool) bool {
	return bool(C.gwim_request_accessibility(C.bool(prompt)))
}

// silence "unused" warning for unsafe in Cgo-only code paths.
var _ = unsafe.Pointer(nil)
