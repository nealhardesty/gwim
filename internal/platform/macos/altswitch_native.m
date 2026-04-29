// altswitch_native.m — macOS native implementation of the Alt-Tab window
// switcher (per ALTTAB.md).
//
// Compiled by cgo as Objective-C alongside the Go files in this package
// because @interface / @implementation cannot live in a cgo preamble (cgo
// preambles are translated as plain C, not ObjC). The Go side declares
// extern symbols and calls into them; the //export'd Go callback
// gwimAltswitchEvent receives event-tap notifications.
//
// Four responsibilities:
//
//  1. Borderless NSWindow overlay drawn from the main thread. Each slot
//     shows a live window thumbnail (when Screen Recording permission is
//     granted) plus the application icon as a small badge; falls back to
//     the app icon alone when capture is unavailable, per ALTTAB §6.
//
//  2. CGEventTap installed only while the overlay is open. Captures Tab,
//     Shift+Tab, Esc, Return, and the Option flag-changed event so the
//     Go controller can advance / commit / cancel.
//
//  3. AX-driven enumeration and raise. Uses _AXUIElementGetWindow (a
//     long-stable private API used by Hammerspoon, Yabai, Rectangle, etc.)
//     to correlate AXUIElementRef with CGWindowID so the MRU stash can
//     identify windows by a stable key.
//
//  4. Screen Recording permission probe + request, mirroring the
//     accessibility-permission helpers used by the rest of GWiM.

#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

// Forward declaration of the Go callback. cgo synthesises this symbol
// from gwimAltswitchEvent's //export directive in altswitch.go.
extern void gwimAltswitchEvent(int kind, int keycode, int optionDown, int shiftDown);

// Private API: maps an AXUIElementRef back to its CGWindowID. Stable
// since Snow Leopard; widely used by open-source window managers.
extern AXError _AXUIElementGetWindow(AXUIElementRef element, CGWindowID *out);

// =====================================================================
// Overlay window
// =====================================================================

// Slot geometry shared between layout (gwim_overlay_show) and drawing
// (GWIMOverlayView drawRect:). Slots are 3:2 to match typical window
// aspect; when no thumbnail is available we draw the app icon centred
// inside the same rectangle so the overlay's geometry doesn't reflow
// based on Screen Recording permission state.
static const CGFloat kGwimSlotW   = 144.0;
static const CGFloat kGwimSlotH   = 96.0;
static const CGFloat kGwimSlotPad = 18.0;
static const CGFloat kGwimTitleH  = 30.0;

@interface GWIMOverlayView : NSView
@property (nonatomic, strong) NSArray *icons;       // NSImage* or NSNull* (always app icons)
@property (nonatomic, strong) NSArray *thumbnails;  // NSImage* or NSNull* (one per slot)
@property (nonatomic, strong) NSArray *titles;      // NSString*
@property (nonatomic, strong) NSArray *appNames;    // NSString*
@property (nonatomic) NSInteger selected;
@property (nonatomic) NSInteger cols;
@end

@implementation GWIMOverlayView

- (BOOL)isFlipped { return NO; }

// aspectFitRect returns the largest rectangle inside `bounds` that has
// the same aspect ratio as `imageSize`, centred within bounds.
static NSRect aspectFitRect(NSSize imageSize, NSRect bounds) {
    if (imageSize.width <= 0 || imageSize.height <= 0) return bounds;
    CGFloat sw = bounds.size.width / imageSize.width;
    CGFloat sh = bounds.size.height / imageSize.height;
    CGFloat s  = sw < sh ? sw : sh;
    CGFloat w  = imageSize.width * s;
    CGFloat h  = imageSize.height * s;
    return NSMakeRect(bounds.origin.x + (bounds.size.width  - w) / 2.0,
                      bounds.origin.y + (bounds.size.height - h) / 2.0,
                      w, h);
}

- (void)drawRect:(NSRect)dirty {
    [[NSColor clearColor] setFill];
    NSRectFill(self.bounds);

    // Background panel — slightly translucent dark.
    [[NSColor colorWithCalibratedWhite:0.10 alpha:0.92] setFill];
    NSBezierPath *bg = [NSBezierPath bezierPathWithRoundedRect:self.bounds
                                                       xRadius:18 yRadius:18];
    [bg fill];

    NSInteger n = (NSInteger)[self.icons count];
    if (n == 0) return;

    const CGFloat slotW = kGwimSlotW;
    const CGFloat slotH = kGwimSlotH;
    const CGFloat pad   = kGwimSlotPad;
    const CGFloat titleH = kGwimTitleH;

    NSInteger cols = self.cols > 0 ? self.cols : n;
    if (cols > n) cols = n;
    NSInteger rows = (n + cols - 1) / cols;

    CGFloat gridW = cols * slotW + (cols - 1) * pad;
    CGFloat gridH = rows * slotH + (rows - 1) * pad;
    CGFloat startX = (self.bounds.size.width - gridW) / 2.0;
    CGFloat startY = (self.bounds.size.height + gridH) / 2.0 + titleH / 2.0;

    for (NSInteger i = 0; i < n; i++) {
        NSInteger row = i / cols;
        NSInteger col = i % cols;
        CGFloat x = startX + col * (slotW + pad);
        CGFloat y = startY - (row + 1) * slotH - row * pad;
        NSRect slotRect = NSMakeRect(x, y, slotW, slotH);

        if (i == self.selected) {
            NSRect ring = NSInsetRect(slotRect, -10, -10);
            [[NSColor colorWithCalibratedWhite:1.0 alpha:0.20] setFill];
            NSBezierPath *r = [NSBezierPath bezierPathWithRoundedRect:ring
                                                              xRadius:14 yRadius:14];
            [r fill];
            [[NSColor whiteColor] setStroke];
            [r setLineWidth:3.0];
            [r stroke];
        }

        id thumbObj = (i < (NSInteger)self.thumbnails.count)
            ? self.thumbnails[i] : (id)[NSNull null];
        id iconObj  = self.icons[i];
        BOOL haveThumb = [thumbObj isKindOfClass:[NSImage class]];
        BOOL haveIcon  = [iconObj  isKindOfClass:[NSImage class]];

        if (haveThumb) {
            // Inset slightly so a thumbnail doesn't overlap the selection
            // ring; aspect-fit so window proportions are preserved.
            NSRect thumbBounds = NSInsetRect(slotRect, 4, 4);
            NSImage *thumb = (NSImage *)thumbObj;
            NSRect drawRect = aspectFitRect([thumb size], thumbBounds);

            // Soft rounded clip + subtle background so letterbox bars are
            // less obtrusive when the thumbnail isn't 3:2.
            [[NSColor colorWithCalibratedWhite:0.0 alpha:0.55] setFill];
            NSBezierPath *frame = [NSBezierPath bezierPathWithRoundedRect:thumbBounds
                                                                  xRadius:6 yRadius:6];
            [frame fill];

            [NSGraphicsContext saveGraphicsState];
            [frame addClip];
            [thumb drawInRect:drawRect
                     fromRect:NSZeroRect
                    operation:NSCompositingOperationSourceOver
                     fraction:1.0];
            [NSGraphicsContext restoreGraphicsState];

            // App icon badge in the bottom-right corner.
            if (haveIcon) {
                const CGFloat badge = 28.0;
                NSRect badgeRect = NSMakeRect(
                    NSMaxX(slotRect) - badge - 6,
                    NSMinY(slotRect) + 6,
                    badge, badge);
                [(NSImage *)iconObj drawInRect:badgeRect
                                      fromRect:NSZeroRect
                                     operation:NSCompositingOperationSourceOver
                                      fraction:1.0];
            }
        } else if (haveIcon) {
            // No thumbnail — fall back to the app icon centred large.
            const CGFloat iconSize = 64.0;
            NSRect iconRect = NSMakeRect(
                slotRect.origin.x + (slotW - iconSize) / 2.0,
                slotRect.origin.y + (slotH - iconSize) / 2.0,
                iconSize, iconSize);
            [(NSImage *)iconObj drawInRect:iconRect
                                  fromRect:NSZeroRect
                                 operation:NSCompositingOperationSourceOver
                                  fraction:1.0];
        } else {
            // No icon and no thumbnail — placeholder so the slot is visible.
            [[NSColor colorWithCalibratedWhite:0.4 alpha:1.0] setFill];
            NSBezierPath *p = [NSBezierPath bezierPathWithRoundedRect:slotRect
                                                              xRadius:10 yRadius:10];
            [p fill];
        }
    }

    // Title for currently selected.
    if (self.selected >= 0 && self.selected < n) {
        NSString *t = (NSString *)self.titles[self.selected];
        NSString *a = (NSString *)self.appNames[self.selected];
        NSString *full = (t.length > 0)
            ? [NSString stringWithFormat:@"%@ — %@", a, t]
            : a;
        NSDictionary *attrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:14
                                                   weight:NSFontWeightMedium],
            NSForegroundColorAttributeName: [NSColor whiteColor],
        };
        NSSize sz = [full sizeWithAttributes:attrs];
        CGFloat tx = (self.bounds.size.width - sz.width) / 2.0;
        if (tx < pad) tx = pad;
        CGFloat ty = pad / 2.0;
        [full drawAtPoint:NSMakePoint(tx, ty) withAttributes:attrs];
    }
}

@end

static NSWindow       *gOverlayWindow = nil;
static GWIMOverlayView *gOverlayView   = nil;

// gwim_capture_thumbnails snapshots a batch of windows by CGWindowID
// using ScreenCaptureKit (macOS 14+). Returns an NSArray of `count`
// objects, each either an NSImage* or NSNull* on failure (denied
// permission, occluded, owning app exited between enumeration and
// capture, etc.). The pre-macOS-14 path returns all-NSNull*; users on
// older releases get the icon-only fallback.
//
// CGWindowListCreateImage was the obvious fit here but Apple obsoleted
// it in the macOS 15 SDK — the symbol is no longer linkable. SCK is the
// only forward-compatible option.
//
// SCK's capture API is async, so we bridge to sync via dispatch
// semaphores. This function is called from a goroutine (showOverlay
// runs off the Carbon hotkey dispatch goroutine), never from the main
// thread, so the blocking is harmless. Per-window timeout is 1 s; the
// up-front getShareableContent call gets 2 s.
//
// Cost ballpark: getShareableContent ≈ 10–40 ms; each capture ≈ 5–15 ms.
// For ~10 windows total switcher-open latency is ~100–200 ms. If that
// becomes a concern we can issue captures concurrently.
static NSArray *gwim_capture_thumbnails(int *cgids, int count) {
    NSMutableArray *out = [NSMutableArray arrayWithCapacity:count];
    for (int i = 0; i < count; i++) [out addObject:[NSNull null]];
    if (count == 0) return out;

    if (@available(macOS 14, *)) {
        __block SCShareableContent *content = nil;
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);
        [SCShareableContent getShareableContentExcludingDesktopWindows:NO
                                                  onScreenWindowsOnly:YES
                                                    completionHandler:
            ^(SCShareableContent * _Nullable c, NSError * _Nullable error) {
                (void)error;
                content = c;
                dispatch_semaphore_signal(sem);
            }];
        if (dispatch_semaphore_wait(sem,
                dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC)) != 0) {
            return out;
        }
        if (content == nil) return out;

        NSMutableDictionary<NSNumber *, SCWindow *> *wmap =
            [NSMutableDictionary dictionary];
        for (SCWindow *w in content.windows) {
            wmap[@(w.windowID)] = w;
        }

        for (int i = 0; i < count; i++) {
            uint32_t cgid = (uint32_t)cgids[i];
            if (cgid == 0) continue;
            SCWindow *target = wmap[@(cgid)];
            if (target == nil) continue;

            SCContentFilter *filter = [[SCContentFilter alloc]
                initWithDesktopIndependentWindow:target];
            SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
            NSInteger w = (NSInteger)target.frame.size.width;
            NSInteger h = (NSInteger)target.frame.size.height;
            config.width  = w > 0 ? w : 320;
            config.height = h > 0 ? h : 200;
            config.scalesToFit = YES;
            config.showsCursor = NO;

            __block CGImageRef img = NULL;
            dispatch_semaphore_t sem2 = dispatch_semaphore_create(0);
            [SCScreenshotManager captureImageWithFilter:filter
                                          configuration:config
                                      completionHandler:
                ^(CGImageRef _Nullable image, NSError * _Nullable error) {
                    (void)error;
                    if (image != NULL) img = CGImageRetain(image);
                    dispatch_semaphore_signal(sem2);
                }];
            dispatch_semaphore_wait(sem2,
                dispatch_time(DISPATCH_TIME_NOW, 1 * NSEC_PER_SEC));

            if (img != NULL) {
                NSImage *ni = [[NSImage alloc] initWithCGImage:img
                                                          size:NSZeroSize];
                CGImageRelease(img);
                out[i] = ni;
            }
        }
    }
    return out;
}

// gwim_overlay_show builds (or reuses) the overlay NSWindow and displays
// it centred on the primary screen.
//
// Parallel arrays:
//   pids[i]              — owning process pid (used for app icon lookup)
//   cgids[i]             — CGWindowID (used for thumbnail capture; 0 to skip)
//   titles_and_apps[i*2] — window title (UTF-8 C string, may be empty)
//   titles_and_apps[i*2+1] — application localised name (UTF-8 C string)
//
// All C strings are copied into NSStrings before this returns; the caller
// may free them immediately afterwards. Thumbnails are captured
// synchronously inside this call; on a typical session that's <100 ms
// total even for ~10 windows. Capture failure (Screen Recording denied,
// occluded window, etc.) silently falls back to the app icon.
void gwim_overlay_show(int *pids,
                       int *cgids,
                       const char **titles_and_apps,
                       int count,
                       int selected) {
    if (count <= 0) return;

    NSMutableArray *icons    = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *titleArr = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *appArr   = [NSMutableArray arrayWithCapacity:count];

    for (int i = 0; i < count; i++) {
        pid_t pid = (pid_t)pids[i];
        NSRunningApplication *app =
            [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        NSImage *icon = (app != nil) ? [app icon] : nil;
        [icons addObject:(icon != nil ? (id)icon : (id)[NSNull null])];

        const char *t = titles_and_apps[i * 2];
        const char *a = titles_and_apps[i * 2 + 1];
        [titleArr addObject:(t ? [NSString stringWithUTF8String:t] : @"")];
        [appArr   addObject:(a ? [NSString stringWithUTF8String:a] : @"")];
    }

    // Batch-capture thumbnails. Returns NSNull* placeholders for windows
    // we couldn't snapshot (denied permission, occluded, gone), so the
    // overlay falls back to the app icon for those slots.
    NSArray *thumbnails = (cgids != NULL)
        ? gwim_capture_thumbnails(cgids, count)
        : [NSArray array];

    // Lay out: at most 6 columns (slots are wider for thumbnails), wrap
    // into rows for larger window counts.
    int cols = count < 6 ? count : 6;
    int rows = (count + cols - 1) / cols;

    const CGFloat slotW = kGwimSlotW;
    const CGFloat slotH = kGwimSlotH;
    const CGFloat pad   = kGwimSlotPad;
    CGFloat width  = pad * 2 + cols * slotW + (cols - 1) * pad + 40.0;
    CGFloat height = pad * 2 + rows * slotH + (rows - 1) * pad + 50.0;
    if (width  < 320) width  = 320;
    if (height < 180) height = 180;

    NSScreen *primary = [[NSScreen screens] firstObject];
    if (primary == nil) primary = [NSScreen mainScreen];
    NSRect screen = [primary frame];
    CGFloat ox = screen.origin.x + (screen.size.width  - width)  / 2.0;
    CGFloat oy = screen.origin.y + (screen.size.height - height) / 2.0;

    dispatch_block_t block = ^{
        NSRect frame = NSMakeRect(ox, oy, width, height);

        if (gOverlayWindow == nil) {
            gOverlayWindow = [[NSWindow alloc]
                initWithContentRect:frame
                          styleMask:NSWindowStyleMaskBorderless
                            backing:NSBackingStoreBuffered
                              defer:NO];
            [gOverlayWindow setOpaque:NO];
            [gOverlayWindow setBackgroundColor:[NSColor clearColor]];
            [gOverlayWindow setLevel:NSStatusWindowLevel];
            [gOverlayWindow setIgnoresMouseEvents:YES];
            [gOverlayWindow setHasShadow:YES];
            [gOverlayWindow setHidesOnDeactivate:NO];
            [gOverlayWindow setCollectionBehavior:
                NSWindowCollectionBehaviorCanJoinAllSpaces |
                NSWindowCollectionBehaviorStationary       |
                NSWindowCollectionBehaviorIgnoresCycle];

            gOverlayView = [[GWIMOverlayView alloc]
                initWithFrame:NSMakeRect(0, 0, width, height)];
            [gOverlayWindow setContentView:gOverlayView];
        } else {
            [gOverlayWindow setFrame:frame display:NO];
            [gOverlayView   setFrameSize:NSMakeSize(width, height)];
        }

        gOverlayView.icons      = icons;
        gOverlayView.thumbnails = thumbnails;
        gOverlayView.titles     = titleArr;
        gOverlayView.appNames   = appArr;
        gOverlayView.cols       = cols;
        gOverlayView.selected   = selected;
        [gOverlayView setNeedsDisplay:YES];

        [gOverlayWindow orderFrontRegardless];
    };

    if ([NSThread isMainThread]) block();
    else dispatch_sync(dispatch_get_main_queue(), block);
}

void gwim_overlay_update_selected(int idx) {
    dispatch_block_t block = ^{
        if (gOverlayView == nil) return;
        gOverlayView.selected = idx;
        [gOverlayView setNeedsDisplay:YES];
    };
    if ([NSThread isMainThread]) block();
    else dispatch_async(dispatch_get_main_queue(), block);
}

void gwim_overlay_hide(void) {
    dispatch_block_t block = ^{
        if (gOverlayWindow != nil) {
            [gOverlayWindow orderOut:nil];
        }
    };
    if ([NSThread isMainThread]) block();
    else dispatch_async(dispatch_get_main_queue(), block);
}

// =====================================================================
// Event tap
// =====================================================================
//
// Installed only while the overlay is open. Captures Tab, Shift+Tab,
// Esc, Return, and the Option flag transition; suppresses Tab/Esc/Return
// so they never reach the foreground app.
//
// Sits at kCGSessionEventTap with kCGHeadInsertEventTap so Carbon's
// RegisterEventHotKey processing happens AFTER us — that's how the tap
// can intercept Option+Tab while the overlay is open without re-entering
// our own hotkey handler.

static CFMachPortRef     gTap        = NULL;
static CFRunLoopSourceRef gTapSource = NULL;

static CGEventRef gwim_event_tap_callback(CGEventTapProxy proxy,
                                          CGEventType type,
                                          CGEventRef event,
                                          void *refcon) {
    (void)proxy; (void)refcon;

    // macOS auto-disables an event tap if its callback is too slow or the
    // user generates input while we're hung. Re-enable on auto-disable.
    if (type == kCGEventTapDisabledByTimeout ||
        type == kCGEventTapDisabledByUserInput) {
        if (gTap != NULL) CGEventTapEnable(gTap, true);
        return event;
    }

    if (type == kCGEventKeyDown) {
        int kc = (int)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        CGEventFlags flags = CGEventGetFlags(event);
        int optDown   = (flags & kCGEventFlagMaskAlternate) ? 1 : 0;
        int shiftDown = (flags & kCGEventFlagMaskShift)     ? 1 : 0;
        gwimAltswitchEvent(0, kc, optDown, shiftDown);

        // Suppress Tab (48), Esc (53), Return (36), Enter (76).
        if (kc == 48 || kc == 53 || kc == 36 || kc == 76) return NULL;
    } else if (type == kCGEventFlagsChanged) {
        CGEventFlags flags = CGEventGetFlags(event);
        int optDown = (flags & kCGEventFlagMaskAlternate) ? 1 : 0;
        gwimAltswitchEvent(1, 0, optDown, 0);
    }
    return event;
}

bool gwim_eventtap_install(void) {
    if (gTap != NULL) return true;
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) |
                       CGEventMaskBit(kCGEventFlagsChanged);
    gTap = CGEventTapCreate(kCGSessionEventTap,
                            kCGHeadInsertEventTap,
                            kCGEventTapOptionDefault,
                            mask,
                            gwim_event_tap_callback,
                            NULL);
    if (gTap == NULL) return false;
    gTapSource = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, gTap, 0);
    CFRunLoopAddSource(CFRunLoopGetMain(), gTapSource, kCFRunLoopCommonModes);
    CGEventTapEnable(gTap, true);
    return true;
}

void gwim_eventtap_remove(void) {
    if (gTap == NULL) return;
    CGEventTapEnable(gTap, false);
    if (gTapSource != NULL) {
        CFRunLoopRemoveSource(CFRunLoopGetMain(), gTapSource, kCFRunLoopCommonModes);
        CFRelease(gTapSource);
        gTapSource = NULL;
    }
    CFRelease(gTap);
    gTap = NULL;
}

// gwim_option_currently_down reports whether the Option modifier is held
// at the moment of inquiry. Used to distinguish hotkey-style invocations
// (auto-commit on Option release) from tray-click invocations (commit
// requires Return).
bool gwim_option_currently_down(void) {
    CGEventFlags flags = CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState);
    return (flags & kCGEventFlagMaskAlternate) != 0;
}

// =====================================================================
// Window enumeration / raise
// =====================================================================

typedef struct {
    pid_t    pid;
    uint32_t cgid;
    char    *title;     // strdup'd; freed by gwim_free_window_entries
    char    *app_name;  // strdup'd; freed by gwim_free_window_entries
} gwim_window_entry;

// dup_cfstring_utf8 returns a strdup'd UTF-8 copy of a CFString, or NULL.
static char *dup_cfstring_utf8(CFStringRef s) {
    if (s == NULL) return NULL;
    const char *fast = CFStringGetCStringPtr(s, kCFStringEncodingUTF8);
    if (fast != NULL) return strdup(fast);
    CFIndex len = CFStringGetLength(s);
    CFIndex max = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
    char *buf = (char *)malloc((size_t)max);
    if (buf == NULL) return NULL;
    if (!CFStringGetCString(s, buf, max, kCFStringEncodingUTF8)) {
        free(buf);
        return NULL;
    }
    return buf;
}

// gwim_enumerate_windows fills out_arr with up to max entries describing
// every "standard" window across all regular running apps. Returns the
// number written. Caller MUST call gwim_free_window_entries to free the
// per-entry strdup'd strings.
//
// out_focused_pid / out_focused_cgid hold the currently focused window
// (so the controller can pin it to MRU position 0).
int gwim_enumerate_windows(gwim_window_entry *out_arr,
                            int max,
                            pid_t *out_focused_pid,
                            uint32_t *out_focused_cgid) {
    int count = 0;
    if (out_focused_pid)  *out_focused_pid  = 0;
    if (out_focused_cgid) *out_focused_cgid = 0;

    // Discover the focused window via the system-wide AX element (the
    // same path window.go uses for SetFrame). Done first so we always
    // know the pin target even if enumeration is later truncated.
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    if (sys != NULL) {
        CFTypeRef appRef = NULL;
        if (AXUIElementCopyAttributeValue(sys, kAXFocusedApplicationAttribute, &appRef)
            == kAXErrorSuccess && appRef != NULL) {
            CFTypeRef winRef = NULL;
            if (AXUIElementCopyAttributeValue((AXUIElementRef)appRef,
                                              kAXFocusedWindowAttribute, &winRef)
                == kAXErrorSuccess && winRef != NULL) {
                CGWindowID cg = 0;
                if (_AXUIElementGetWindow((AXUIElementRef)winRef, &cg) == kAXErrorSuccess
                    && out_focused_cgid) {
                    *out_focused_cgid = (uint32_t)cg;
                }
                pid_t fpid = 0;
                if (AXUIElementGetPid((AXUIElementRef)winRef, &fpid) == kAXErrorSuccess
                    && out_focused_pid) {
                    *out_focused_pid = fpid;
                }
                CFRelease(winRef);
            }
            CFRelease(appRef);
        }
        CFRelease(sys);
    }

    NSArray<NSRunningApplication *> *apps =
        [[NSWorkspace sharedWorkspace] runningApplications];
    for (NSRunningApplication *app in apps) {
        if (count >= max) break;
        if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;
        pid_t pid = [app processIdentifier];
        if (pid <= 0) continue;

        AXUIElementRef appAX = AXUIElementCreateApplication(pid);
        if (appAX == NULL) continue;

        CFTypeRef windowsRef = NULL;
        AXError err = AXUIElementCopyAttributeValue(appAX, kAXWindowsAttribute,
                                                    &windowsRef);
        if (err != kAXErrorSuccess || windowsRef == NULL) {
            CFRelease(appAX);
            continue;
        }

        CFArrayRef windows = (CFArrayRef)windowsRef;
        CFIndex n = CFArrayGetCount(windows);
        for (CFIndex i = 0; i < n && count < max; i++) {
            AXUIElementRef win =
                (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);

            // Only standard windows: dialogs, sheets, utility panels, and
            // popovers don't belong in an Alt-Tab list.
            CFTypeRef subroleRef = NULL;
            BOOL isStandard = NO;
            if (AXUIElementCopyAttributeValue(win, kAXSubroleAttribute, &subroleRef)
                == kAXErrorSuccess && subroleRef != NULL) {
                if (CFGetTypeID(subroleRef) == CFStringGetTypeID() &&
                    CFStringCompare((CFStringRef)subroleRef,
                                    kAXStandardWindowSubrole, 0)
                    == kCFCompareEqualTo) {
                    isStandard = YES;
                }
                CFRelease(subroleRef);
            }
            if (!isStandard) continue;

            // Skip minimised windows — they aren't directly raisable to
            // visible state from AX without unminimising, which the user
            // didn't ask for in this MVP.
            CFTypeRef minRef = NULL;
            BOOL isMin = NO;
            if (AXUIElementCopyAttributeValue(win, kAXMinimizedAttribute, &minRef)
                == kAXErrorSuccess && minRef != NULL) {
                if (CFGetTypeID(minRef) == CFBooleanGetTypeID()) {
                    isMin = CFBooleanGetValue((CFBooleanRef)minRef);
                }
                CFRelease(minRef);
            }
            if (isMin) continue;

            CGWindowID cgid = 0;
            if (_AXUIElementGetWindow(win, &cgid) != kAXErrorSuccess || cgid == 0) {
                continue;
            }

            CFTypeRef titleRef = NULL;
            char *title = NULL;
            if (AXUIElementCopyAttributeValue(win, kAXTitleAttribute, &titleRef)
                == kAXErrorSuccess && titleRef != NULL) {
                if (CFGetTypeID(titleRef) == CFStringGetTypeID()) {
                    title = dup_cfstring_utf8((CFStringRef)titleRef);
                }
                CFRelease(titleRef);
            }
            if (title == NULL) title = strdup("");

            char *appName = NULL;
            NSString *localized = [app localizedName];
            if (localized != nil) {
                const char *u = [localized UTF8String];
                if (u != NULL) appName = strdup(u);
            }
            if (appName == NULL) appName = strdup("");

            out_arr[count].pid      = pid;
            out_arr[count].cgid     = (uint32_t)cgid;
            out_arr[count].title    = title;
            out_arr[count].app_name = appName;
            count++;
        }
        CFRelease(windowsRef);
        CFRelease(appAX);
    }
    return count;
}

void gwim_free_window_entries(gwim_window_entry *arr, int count) {
    if (arr == NULL) return;
    for (int i = 0; i < count; i++) {
        if (arr[i].title)    free(arr[i].title);
        if (arr[i].app_name) free(arr[i].app_name);
        arr[i].title    = NULL;
        arr[i].app_name = NULL;
    }
}

// gwim_raise_window finds the window with matching CGWindowID inside the
// process pid, raises it via AX, and activates the owning NSRunningApp.
// Returns true on success.
bool gwim_raise_window(pid_t pid, uint32_t cgid) {
    AXUIElementRef appAX = AXUIElementCreateApplication(pid);
    if (appAX == NULL) return false;

    CFTypeRef windowsRef = NULL;
    bool raised = false;
    if (AXUIElementCopyAttributeValue(appAX, kAXWindowsAttribute, &windowsRef)
        == kAXErrorSuccess && windowsRef != NULL) {
        CFArrayRef windows = (CFArrayRef)windowsRef;
        CFIndex n = CFArrayGetCount(windows);
        for (CFIndex i = 0; i < n; i++) {
            AXUIElementRef win =
                (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
            CGWindowID got = 0;
            if (_AXUIElementGetWindow(win, &got) == kAXErrorSuccess &&
                (uint32_t)got == cgid) {
                AXUIElementPerformAction(win, kAXRaiseAction);
                AXUIElementSetAttributeValue(win, kAXMainAttribute,
                                              kCFBooleanTrue);
                raised = true;
                break;
            }
        }
        CFRelease(windowsRef);
    }
    CFRelease(appAX);

    NSRunningApplication *app =
        [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
    if (app != nil) {
        [app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
    }
    return raised;
}

// =====================================================================
// Screen Recording permission
// =====================================================================
//
// Probe with CGPreflightScreenCaptureAccess (silent — never prompts), and
// trigger the system prompt with CGRequestScreenCaptureAccess. Available
// on macOS 10.15+. On older releases we report "granted" so the rest of
// the code degrades to the legacy "no permission gate" behaviour.
//
// The request call adds GWiM to System Settings → Privacy & Security →
// Screen Recording on first use; the user still has to flip the toggle.
// macOS 14 requires the host app be quit and relaunched after toggling,
// which the tray's "click to fix" hint reflects.

bool gwim_screen_recording_granted(void) {
    if (@available(macOS 10.15, *)) {
        return CGPreflightScreenCaptureAccess();
    }
    return true;
}

bool gwim_screen_recording_request(void) {
    if (@available(macOS 10.15, *)) {
        return CGRequestScreenCaptureAccess();
    }
    return true;
}
