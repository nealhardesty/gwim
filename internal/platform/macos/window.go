//go:build darwin

package macos

/*
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <math.h>

// Forward declaration — implementation lives further down with the rest
// of the AX helpers.
static bool gwim_ax_debug_enabled(void);

// gwim_ax_query_focused_window walks an AXUIElementRef for an
// application down to its kAXFocusedWindow, performing the
// AXManualAccessibility opt-in dance for Chromium-style apps along the
// way. Returns NULL if no focused window is reachable.
//
// Chromium / Electron (Chrome, Slack, Edge, Brave, VS Code, Discord,
// …): since Chromium 88 the renderer's AX tree is opt-in. Until an
// external client sets AXManualAccessibility = true on the app
// element, kAXFocusedWindow / kAXWindows return nothing. We probe via
// the normal path; if it returns nothing AND the app exposes
// AXManualAccessibility, we flip it on, give the renderer a beat to
// populate the AX tree, and retry. Same trick AltTab.app /
// Hammerspoon / Yabai use. The attribute is left on intentionally —
// toggling it off again would re-empty the AX tree.
static AXUIElementRef gwim_ax_query_focused_window(AXUIElementRef app, const char *via) {
    if (app == NULL) return NULL;
    pid_t pid = 0;
    AXUIElementGetPid(app, &pid);

    CFTypeRef windowRef = NULL;
    AXError err = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, &windowRef);
    if (err == kAXErrorSuccess && windowRef != NULL) {
        return (AXUIElementRef)windowRef;
    }

    CFTypeRef maProbe = NULL;
    AXError maErr = AXUIElementCopyAttributeValue(app, CFSTR("AXManualAccessibility"), &maProbe);
    if (maProbe != NULL) CFRelease(maProbe);
    bool optedIn = false;
    int polls = 0;
    if (maErr == kAXErrorSuccess) {
        AXError setErr = AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), kCFBooleanTrue);
        optedIn = true;
        if (gwim_ax_debug_enabled()) {
            fprintf(stderr,
                    "gwim_ax_query_focused_window via=%s pid=%d firstErr=%d setErr=%d (opting in via AXManualAccessibility)\n",
                    via, (int)pid, (int)err, (int)setErr);
            fflush(stderr);
        }
        for (int i = 0; i < 8; i++) {
            polls++;
            usleep(25 * 1000); // 25ms × 8 = up to 200ms
            windowRef = NULL;
            err = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, &windowRef);
            if (err == kAXErrorSuccess && windowRef != NULL) {
                if (gwim_ax_debug_enabled()) {
                    fprintf(stderr,
                            "gwim_ax_query_focused_window via=%s pid=%d polls=%d (recovered)\n",
                            via, (int)pid, polls);
                    fflush(stderr);
                }
                return (AXUIElementRef)windowRef;
            }
        }
    }

    if (gwim_ax_debug_enabled()) {
        fprintf(stderr,
                "gwim_ax_query_focused_window via=%s pid=%d firstErr=%d maProbeErr=%d optedIn=%d polls=%d (no focused window)\n",
                via, (int)pid, (int)err, (int)maErr, (int)optedIn, polls);
        fflush(stderr);
    }
    return NULL;
}

// gwim_ax_focused_window returns the AXUIElementRef of the currently
// focused window, or NULL if none. Callers MUST CFRelease the returned
// reference.
//
// Strategy: try two independent paths and use whichever finds a window.
//
//  1. SystemWide -> kAXFocusedApplicationAttribute. The traditional
//     reliable path: survives Spaces transitions and apps that lie
//     about being foreground (e.g. background notification panels).
//
//  2. NSWorkspace.frontmostApplication.processIdentifier ->
//     AXUIElementCreateApplication(pid). Required fallback because on
//     macOS 26 (Tahoe) the system-wide AX query returns
//     kAXErrorCannotComplete (-25212) for Chrome and other Chromium /
//     Electron apps — the AX subsystem refuses to identify them as
//     the focused app. NSWorkspace doesn't share this limitation. We
//     keep both paths because the system-wide query is faster on
//     well-behaved apps and survives weird focus edge cases the
//     NSWorkspace path would mishandle. AltTab.app and Yabai use the
//     same belt-and-braces approach.
//
// Each candidate app element is walked via gwim_ax_query_focused_window
// which also handles the Chromium AXManualAccessibility opt-in for the
// renderers that need it.
static AXUIElementRef gwim_ax_focused_window(void) {
    bool dbg = gwim_ax_debug_enabled();

    // Path 1: system-wide.
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    AXError sysErr = kAXErrorFailure;
    if (sys != NULL) {
        CFTypeRef appRef = NULL;
        sysErr = AXUIElementCopyAttributeValue(sys, kAXFocusedApplicationAttribute, &appRef);
        CFRelease(sys);
        if (sysErr == kAXErrorSuccess && appRef != NULL) {
            AXUIElementRef win = gwim_ax_query_focused_window((AXUIElementRef)appRef, "systemwide");
            CFRelease(appRef);
            if (win != NULL) return win;
        } else if (appRef != NULL) {
            // Some macOS versions write a non-NULL value AND return an
            // error; release defensively.
            CFRelease(appRef);
        }
    }

    // Path 2: NSWorkspace.frontmostApplication fallback. Log once per
    // process when we have to use it so the diagnostic output stays
    // legible — the systemwide failure repeats on every keystroke on
    // affected macOS versions and we don't want to flood stderr.
    static bool fallbackLogged = false;

    NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    if (front == nil) {
        if (dbg) {
            fprintf(stderr, "gwim_ax_focused_window: NSWorkspace.frontmostApplication is nil\n");
            fflush(stderr);
        }
        return NULL;
    }
    pid_t pid = [front processIdentifier];
    if (dbg && !fallbackLogged) {
        const char *bundle = [[front bundleIdentifier] UTF8String];
        fprintf(stderr,
                "gwim_ax_focused_window: systemwide path returned err=%d, falling back to NSWorkspace "
                "(first occurrence; pid=%d bundle=%s — further fallbacks suppressed)\n",
                (int)sysErr, (int)pid, bundle ? bundle : "(nil)");
        fflush(stderr);
        fallbackLogged = true;
    }
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (app == NULL) return NULL;
    AXUIElementRef win = gwim_ax_query_focused_window(app, "nsworkspace");
    CFRelease(app);
    return win;
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

// gwim_ax_app_for_window returns the AXUIElementRef for the window's
// owning application, or NULL on failure. Caller must CFRelease.
//
// We need the parent application element (not the window) to read and
// write kAXEnhancedUserInterfaceAttribute / kAXManualAccessibility —
// those attributes live on the app, not on individual windows.
static AXUIElementRef gwim_ax_app_for_window(AXUIElementRef win) {
    pid_t pid = 0;
    if (AXUIElementGetPid(win, &pid) != kAXErrorSuccess || pid == 0) return NULL;
    return AXUIElementCreateApplication(pid);
}

// gwim_ax_get_bool_attr reads a CFBoolean attribute on the supplied
// element. out_present is set to true if the attribute exists at all (so
// callers know whether they need to restore it on the way out).
static bool gwim_ax_get_bool_attr(AXUIElementRef elem, CFStringRef attr, bool *out_present) {
    *out_present = false;
    if (elem == NULL) return false;
    CFTypeRef val = NULL;
    if (AXUIElementCopyAttributeValue(elem, attr, &val) != kAXErrorSuccess || val == NULL) {
        return false;
    }
    *out_present = true;
    bool b = (CFGetTypeID(val) == CFBooleanGetTypeID()) && CFBooleanGetValue((CFBooleanRef)val);
    CFRelease(val);
    return b;
}

// gwim_ax_set_bool_attr writes a CFBoolean attribute on the supplied
// element. Errors are intentionally ignored — the toggle is best-effort
// and the caller restores the prior value on the way out regardless.
static void gwim_ax_set_bool_attr(AXUIElementRef elem, CFStringRef attr, bool v) {
    if (elem == NULL) return;
    AXUIElementSetAttributeValue(elem, attr, v ? kCFBooleanTrue : kCFBooleanFalse);
}

// Tunable: the GWIM_AX_DEBUG=1 environment variable enables NSLog-based
// diagnostics for every gwim_ax_set_frame call. Visible via Console.app
// or stderr when GWiM is launched from Terminal. Off by default to keep
// system logs clean.
static bool gwim_ax_debug_enabled(void) {
    static int cached = -1;
    if (cached < 0) {
        const char *env = getenv("GWIM_AX_DEBUG");
        cached = (env != NULL && env[0] != '\0' && env[0] != '0') ? 1 : 0;
        if (cached == 1) {
            fprintf(stderr, "gwim: GWIM_AX_DEBUG=1 — AX diagnostics enabled\n");
            fflush(stderr);
        }
    }
    return cached == 1;
}

// gwim_ax_set_frame moves and resizes a window via AX, applying the
// Hammerspoon `setFrameCorrectness` workaround for Chromium / Electron
// apps and a defensive read-back/retry for the rest.
//
// AXEnhancedUserInterface: Chrome, Slack, Edge, Brave, VS Code,
// Discord, and friends expose this attribute on their application AX
// element. While it is true the macOS Accessibility server silently
// drops kAXPosition / kAXSize writes on the app's windows — the calls
// return kAXErrorSuccess but the window never actually moves. The
// accepted fix (Hammerspoon `setFrameCorrectness`) is to temporarily
// flip the attribute off, write the geometry, then restore.
//
// AXManualAccessibility: a separate Chromium attribute that opts the
// renderer INTO exposing its AX tree. We deliberately do NOT toggle
// this here — it is set to true once in gwim_ax_focused_window() for
// Chromium-style apps and must stay on, otherwise the AX tree empties
// and subsequent queries fail.
//
// Implementation notes:
//   - Toggling EUI is asynchronous in the target app; we sleep ~15ms
//     after disabling so Chrome's AX server has time to reconfigure
//     before the writes land.
//   - Write order is position -> size -> position. With EUI off this
//     is the canonical Hammerspoon order; the trailing position write
//     defends against apps that clamp position when size changes.
//   - We re-read the realized frame and retry once with a fresh write
//     if the result diverges by more than a couple of points. Belt
//     and braces for apps we don't yet know about.
//
// Set GWIM_AX_DEBUG=1 in the environment to log per-call diagnostics
// (NSLog → Console.app / stderr) when chasing app-specific bugs.
static bool gwim_ax_set_frame(AXUIElementRef win, double x, double y, double w, double h) {
    AXUIElementRef app = gwim_ax_app_for_window(win);
    pid_t pid = 0;
    if (app != NULL) AXUIElementGetPid(app, &pid);

    bool euiPresent = false, euiWasOn = false;
    if (app != NULL) {
        euiWasOn = gwim_ax_get_bool_attr(app, CFSTR("AXEnhancedUserInterface"), &euiPresent);
    }
    if (euiPresent && euiWasOn) {
        gwim_ax_set_bool_attr(app, CFSTR("AXEnhancedUserInterface"), false);
        // 15ms is empirically enough for Chrome to flush its AX state.
        usleep(15 * 1000);
    }

    CGPoint pos = (CGPoint){ .x = x, .y = y };
    CGSize  sz  = (CGSize){ .width = w, .height = h };
    AXValueRef posVal  = AXValueCreate(kAXValueCGPointType, &pos);
    AXValueRef sizeVal = AXValueCreate(kAXValueCGSizeType, &sz);

    AXError e1 = kAXErrorSuccess, e2 = kAXErrorSuccess, e3 = kAXErrorSuccess;
    bool ok = false;
    if (posVal != NULL && sizeVal != NULL) {
        e1 = AXUIElementSetAttributeValue(win, kAXPositionAttribute, posVal);
        e2 = AXUIElementSetAttributeValue(win, kAXSizeAttribute,     sizeVal);
        e3 = AXUIElementSetAttributeValue(win, kAXPositionAttribute, posVal);
        ok = (e1 == kAXErrorSuccess)
          && (e2 == kAXErrorSuccess || e2 == kAXErrorNotImplemented)
          && (e3 == kAXErrorSuccess);
    }

    // Read-back: did the writes actually land? If not, retry once.
    double rx = 0, ry = 0, rw = 0, rh = 0;
    bool readBackOK = gwim_ax_get_frame(win, &rx, &ry, &rw, &rh);
    bool drifted = readBackOK
                && (fabs(rx - x) > 2 || fabs(ry - y) > 2
                 || fabs(rw - w) > 2 || fabs(rh - h) > 2);
    if (drifted && posVal != NULL && sizeVal != NULL) {
        AXUIElementSetAttributeValue(win, kAXPositionAttribute, posVal);
        AXUIElementSetAttributeValue(win, kAXSizeAttribute,     sizeVal);
        AXUIElementSetAttributeValue(win, kAXPositionAttribute, posVal);
        readBackOK = gwim_ax_get_frame(win, &rx, &ry, &rw, &rh);
    }

    if (gwim_ax_debug_enabled()) {
        fprintf(stderr,
                "gwim_ax_set_frame pid=%d eui(present=%d,was=%d) "
                "req=(%.0f,%.0f,%.0fx%.0f) errs=(%d,%d,%d) "
                "realized=(%.0f,%.0f,%.0fx%.0f,readOK=%d) drifted=%d\n",
                (int)pid, euiPresent, euiWasOn,
                x, y, w, h, (int)e1, (int)e2, (int)e3,
                rx, ry, rw, rh, (int)readBackOK, (int)drifted);
        fflush(stderr);
    }

    if (sizeVal) CFRelease(sizeVal);
    if (posVal)  CFRelease(posVal);

    if (euiPresent && euiWasOn) gwim_ax_set_bool_attr(app, CFSTR("AXEnhancedUserInterface"), true);
    if (app) CFRelease(app);

    return ok || readBackOK;
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
