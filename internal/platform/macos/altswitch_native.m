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
//  1. Borderless NSWindow overlay(s) on the main thread — one mirrored
//     panel per NSScreen so the switcher is visible on every display.
//     Each slot shows a live window thumbnail (when Screen Recording
//     permission is granted) plus the application icon as a small badge;
//     falls back to the app icon alone when capture is unavailable, per
//     ALTTAB §6.
//
//  2. CGEventTap installed only while the overlay is open. Captures Tab,
//     Shift+Tab, Esc, Return, and the Option flag-changed event so the
//     Go controller can advance / commit / cancel. Overlay windows accept
//     mouse events; clicking a slot commits like Return.
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
#include <unistd.h>

// Forward declaration of the Go callback. cgo synthesises this symbol
// from gwimAltswitchEvent's //export directive in altswitch.go.
extern void gwimAltswitchEvent(int kind, int keycode, int optionDown, int shiftDown);
extern void gwimAltswitchSlotClicked(int idx);

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
// Scale overlay so it fits within this fraction of the primary screen visibleFrame.
static const CGFloat kGwimOverlayMaxScreenFraction = 0.9;

/// Slot rectangle for index `i`; must match drawRect: grid math (hit testing + paint).
static NSRect gwim_overlay_slot_rect(NSRect viewBounds, NSInteger i, NSInteger n,
                                       NSInteger cols, CGFloat layoutScale) {
    CGFloat sc = layoutScale;
    if (sc < 1e-6) sc = 1.0;
    const CGFloat slotW  = kGwimSlotW * sc;
    const CGFloat slotH  = kGwimSlotH * sc;
    const CGFloat pad    = kGwimSlotPad * sc;
    const CGFloat titleH = kGwimTitleH * sc;
    NSInteger useCols = cols > 0 ? cols : n;
    if (useCols > n) useCols = n;
    NSInteger rows = (n + useCols - 1) / useCols;
    CGFloat gridW = useCols * slotW + (useCols - 1) * pad;
    CGFloat gridH = rows * slotH + (rows - 1) * pad;
    CGFloat startX = (viewBounds.size.width - gridW) / 2.0;
    CGFloat startY = (viewBounds.size.height + gridH) / 2.0 + titleH / 2.0;
    NSInteger row = i / useCols;
    NSInteger col = i % useCols;
    CGFloat x = startX + col * (slotW + pad);
    CGFloat y = startY - (row + 1) * slotH - row * pad;
    return NSMakeRect(x, y, slotW, slotH);
}

@interface GWIMOverlayView : NSView
@property (nonatomic, strong) NSArray *icons;       // NSImage* or NSNull* (always app icons)
@property (nonatomic, strong) NSArray *thumbnails;  // NSImage* or NSNull* (one per slot)
@property (nonatomic, strong) NSArray *titles;      // NSString*
@property (nonatomic, strong) NSArray *appNames;    // NSString*
/// Boxed BOOL per slot — YES means draw the icon/thumbnail at reduced
/// opacity to indicate the window is minimised or its app is hidden.
@property (nonatomic, strong) NSArray *dimmed;
@property (nonatomic) NSInteger selected;
@property (nonatomic) NSInteger cols;
/// Uniform scale vs kGwimSlot* base geometry (set in gwim_overlay_show).
@property (nonatomic) CGFloat layoutScale;
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

    CGFloat sc = self.layoutScale;
    if (sc < 1e-6) sc = 1.0;

    // Background panel — slightly translucent dark.
    [[NSColor colorWithCalibratedWhite:0.10 alpha:0.92] setFill];
    CGFloat panelR = 18.0 * sc;
    NSBezierPath *bg = [NSBezierPath bezierPathWithRoundedRect:self.bounds
                                                       xRadius:panelR yRadius:panelR];
    [bg fill];

    NSInteger n = (NSInteger)[self.icons count];
    if (n == 0) return;

    const CGFloat slotW  = kGwimSlotW * sc;
    const CGFloat slotH  = kGwimSlotH * sc;
    const CGFloat pad    = kGwimSlotPad * sc;
    const CGFloat titleH = kGwimTitleH * sc;

    NSInteger cols = self.cols > 0 ? self.cols : n;
    if (cols > n) cols = n;

    CGFloat ringOutset = 10.0 * sc;
    CGFloat ringCorner = 14.0 * sc;
    CGFloat ringLine   = MAX(1.0, 3.0 * sc);
    CGFloat thumbInset = 4.0 * sc;
    CGFloat thumbCorner = 6.0 * sc;
    CGFloat badge = 28.0 * sc;
    CGFloat badgeMargin = 6.0 * sc;
    CGFloat iconSize = 64.0 * sc;
    CGFloat placeholderR = 10.0 * sc;
    CGFloat titleFont = MAX(11.0, round(14.0 * sc));

    for (NSInteger i = 0; i < n; i++) {
        NSRect slotRect = gwim_overlay_slot_rect(self.bounds, i, n, cols, sc);

        if (i == self.selected) {
            NSRect ring = NSInsetRect(slotRect, -ringOutset, -ringOutset);
            [[NSColor colorWithCalibratedWhite:1.0 alpha:0.20] setFill];
            NSBezierPath *r = [NSBezierPath bezierPathWithRoundedRect:ring
                                                              xRadius:ringCorner yRadius:ringCorner];
            [r fill];
            [[NSColor whiteColor] setStroke];
            [r setLineWidth:ringLine];
            [r stroke];
        }

        id thumbObj = (i < (NSInteger)self.thumbnails.count)
            ? self.thumbnails[i] : (id)[NSNull null];
        id iconObj  = self.icons[i];
        BOOL haveThumb = [thumbObj isKindOfClass:[NSImage class]];
        BOOL haveIcon  = [iconObj  isKindOfClass:[NSImage class]];

        BOOL slotDimmed = NO;
        if (i < (NSInteger)self.dimmed.count) {
            id d = self.dimmed[i];
            if ([d isKindOfClass:[NSNumber class]]) {
                slotDimmed = [(NSNumber *)d boolValue];
            }
        }
        CGFloat artFraction = slotDimmed ? 0.45 : 1.0;

        if (haveThumb) {
            // Inset slightly so a thumbnail doesn't overlap the selection
            // ring; aspect-fit so window proportions are preserved.
            NSRect thumbBounds = NSInsetRect(slotRect, thumbInset, thumbInset);
            NSImage *thumb = (NSImage *)thumbObj;
            NSRect drawRect = aspectFitRect([thumb size], thumbBounds);

            // Soft rounded clip + subtle background so letterbox bars are
            // less obtrusive when the thumbnail isn't 3:2.
            [[NSColor colorWithCalibratedWhite:0.0 alpha:0.55] setFill];
            NSBezierPath *frame = [NSBezierPath bezierPathWithRoundedRect:thumbBounds
                                                                  xRadius:thumbCorner yRadius:thumbCorner];
            [frame fill];

            [NSGraphicsContext saveGraphicsState];
            [frame addClip];
            [thumb drawInRect:drawRect
                     fromRect:NSZeroRect
                    operation:NSCompositingOperationSourceOver
                     fraction:artFraction];
            [NSGraphicsContext restoreGraphicsState];

            // App icon badge in the bottom-right corner.
            if (haveIcon) {
                NSRect badgeRect = NSMakeRect(
                    NSMaxX(slotRect) - badge - badgeMargin,
                    NSMinY(slotRect) + badgeMargin,
                    badge, badge);
                [(NSImage *)iconObj drawInRect:badgeRect
                                      fromRect:NSZeroRect
                                     operation:NSCompositingOperationSourceOver
                                      fraction:artFraction];
            }
        } else if (haveIcon) {
            // No thumbnail — fall back to the app icon centred large.
            NSRect iconRect = NSMakeRect(
                slotRect.origin.x + (slotW - iconSize) / 2.0,
                slotRect.origin.y + (slotH - iconSize) / 2.0,
                iconSize, iconSize);
            [(NSImage *)iconObj drawInRect:iconRect
                                  fromRect:NSZeroRect
                                 operation:NSCompositingOperationSourceOver
                                  fraction:artFraction];
        } else {
            // No icon and no thumbnail — placeholder so the slot is visible.
            [[NSColor colorWithCalibratedWhite:0.4 alpha:1.0] setFill];
            NSBezierPath *p = [NSBezierPath bezierPathWithRoundedRect:slotRect
                                                              xRadius:placeholderR yRadius:placeholderR];
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
            NSFontAttributeName: [NSFont systemFontOfSize:titleFont
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

- (void)mouseUp:(NSEvent *)event {
    NSInteger n = (NSInteger)[self.icons count];
    if (n == 0) return;
    NSInteger cols = self.cols > 0 ? self.cols : n;
    if (cols > n) cols = n;
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    CGFloat sc = self.layoutScale;
    for (NSInteger i = 0; i < n; i++) {
        if (NSPointInRect(p, gwim_overlay_slot_rect(self.bounds, i, n, cols, sc))) {
            gwimAltswitchSlotClicked((int)i);
            return;
        }
    }
}

@end

static NSMutableArray<NSWindow *>       *gOverlayWindows = nil;
static NSMutableArray<GWIMOverlayView *> *gOverlayViews   = nil;

static void gwim_configure_overlay_window(NSWindow *win) {
    [win setOpaque:NO];
    [win setBackgroundColor:[NSColor clearColor]];
    [win setLevel:NSStatusWindowLevel];
    [win setIgnoresMouseEvents:NO];
    [win setHasShadow:YES];
    [win setHidesOnDeactivate:NO];
    [win setCollectionBehavior:
        NSWindowCollectionBehaviorCanJoinAllSpaces |
        NSWindowCollectionBehaviorStationary       |
        NSWindowCollectionBehaviorIgnoresCycle];
}

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

// gwim_overlay_show builds (or reuses) one borderless NSWindow per
// connected NSScreen and displays the same content on each, centred in
// that screen's visibleFrame and scaled up to
// kGwimOverlayMaxScreenFraction of that working area (per display).
//
// Parallel arrays:
//   pids[i]              — owning process pid (used for app icon lookup)
//   cgids[i]             — CGWindowID (used for thumbnail capture; 0 to skip)
//   dimmed_flags[i]      — non-zero ⇒ slot is rendered at reduced opacity
//                          (window is minimised or its app is hidden);
//                          may be NULL ⇒ all slots full opacity.
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
                       int *dimmed_flags,
                       const char **titles_and_apps,
                       int count,
                       int selected) {
    if (count <= 0) return;

    NSMutableArray *icons    = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *titleArr = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *appArr   = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *dimArr   = [NSMutableArray arrayWithCapacity:count];

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

        BOOL dim = (dimmed_flags != NULL) && (dimmed_flags[i] != 0);
        [dimArr addObject:@(dim)];
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

    const CGFloat baseSlotW = kGwimSlotW;
    const CGFloat baseSlotH = kGwimSlotH;
    const CGFloat basePad   = kGwimSlotPad;
    CGFloat intrinsicW =
        basePad * 2 + cols * baseSlotW + (cols - 1) * basePad + 40.0;
    CGFloat intrinsicH =
        basePad * 2 + rows * baseSlotH + (rows - 1) * basePad + 50.0;
    if (intrinsicW < 320) intrinsicW = 320;
    if (intrinsicH < 180) intrinsicH = 180;

    dispatch_block_t block = ^{
        NSArray<NSScreen *> *screenList = [NSScreen screens];
        if (screenList == nil || screenList.count == 0) {
            NSScreen *ms = [NSScreen mainScreen];
            screenList = ms ? @[ ms ] : @[];
        }
        if (screenList.count == 0) return;

        NSUInteger n = screenList.count;

        if (gOverlayWindows == nil) {
            gOverlayWindows = [NSMutableArray array];
            gOverlayViews = [NSMutableArray array];
        }

        while (gOverlayWindows.count > n) {
            NSWindow *w = gOverlayWindows.lastObject;
            [w orderOut:nil];
            [w close];
            [gOverlayWindows removeLastObject];
            [gOverlayViews removeLastObject];
        }

        while (gOverlayWindows.count < n) {
            NSRect r = NSMakeRect(0, 0, 320, 240);
            NSWindow *win = [[NSWindow alloc]
                initWithContentRect:r
                          styleMask:NSWindowStyleMaskBorderless
                            backing:NSBackingStoreBuffered
                              defer:NO];
            gwim_configure_overlay_window(win);
            GWIMOverlayView *v =
                [[GWIMOverlayView alloc] initWithFrame:NSMakeRect(0, 0, 320, 240)];
            [win setContentView:v];
            [gOverlayWindows addObject:win];
            [gOverlayViews addObject:v];
        }

        for (NSUInteger i = 0; i < n; i++) {
            NSScreen *scr = screenList[i];
            NSRect vf = [scr visibleFrame];
            CGFloat s =
                MIN(kGwimOverlayMaxScreenFraction * vf.size.width / intrinsicW,
                    kGwimOverlayMaxScreenFraction * vf.size.height / intrinsicH);
            CGFloat width = intrinsicW * s;
            CGFloat height = intrinsicH * s;
            CGFloat ox = NSMinX(vf) + (NSWidth(vf) - width) / 2.0;
            CGFloat oy = NSMinY(vf) + (NSHeight(vf) - height) / 2.0;
            NSRect frame = NSMakeRect(ox, oy, width, height);

            NSWindow *win = gOverlayWindows[i];
            GWIMOverlayView *view = gOverlayViews[i];
            [win setFrame:frame display:NO];
            [view setFrameSize:NSMakeSize(width, height)];

            view.icons = icons;
            view.thumbnails = thumbnails;
            view.titles = titleArr;
            view.appNames = appArr;
            view.dimmed = dimArr;
            view.cols = cols;
            view.selected = selected;
            view.layoutScale = s;
            [view setNeedsDisplay:YES];

            [win orderFrontRegardless];
        }
    };

    if ([NSThread isMainThread]) block();
    else dispatch_sync(dispatch_get_main_queue(), block);
}

void gwim_overlay_update_selected(int idx) {
    dispatch_block_t block = ^{
        if (gOverlayViews == nil || gOverlayViews.count == 0) return;
        for (GWIMOverlayView *view in gOverlayViews) {
            view.selected = idx;
            [view setNeedsDisplay:YES];
        }
    };
    if ([NSThread isMainThread]) block();
    else dispatch_async(dispatch_get_main_queue(), block);
}

void gwim_overlay_hide(void) {
    dispatch_block_t block = ^{
        if (gOverlayWindows == nil) return;
        for (NSWindow *w in gOverlayWindows) {
            [w orderOut:nil];
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
    bool     minimized; // window is currently minimized to the Dock
    bool     hidden;    // owning app is hidden via Cmd+H / NSRunningApplication.hide
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

        BOOL appHidden = [app isHidden];

        // Resolve the app's localised name once; reused for every entry
        // we emit on this app (including the CGWindowList fallback).
        char *appName = NULL;
        NSString *localized = [app localizedName];
        if (localized != nil) {
            const char *u = [localized UTF8String];
            if (u != NULL) appName = strdup(u);
        }
        if (appName == NULL) appName = strdup("");

        AXUIElementRef appAX = AXUIElementCreateApplication(pid);
        if (appAX == NULL) {
            free(appName);
            continue;
        }

        CFTypeRef windowsRef = NULL;
        AXError err = AXUIElementCopyAttributeValue(appAX, kAXWindowsAttribute,
                                                    &windowsRef);
        int axEmittedForApp = 0;
        if (err == kAXErrorSuccess && windowsRef != NULL) {
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

                // Track minimised state but no longer skip — gwim_raise_window
                // un-minimises before raising, and the overlay dims the slot
                // so users can still tell at a glance.
                CFTypeRef minRef = NULL;
                BOOL isMin = NO;
                if (AXUIElementCopyAttributeValue(win, kAXMinimizedAttribute, &minRef)
                    == kAXErrorSuccess && minRef != NULL) {
                    if (CFGetTypeID(minRef) == CFBooleanGetTypeID()) {
                        isMin = CFBooleanGetValue((CFBooleanRef)minRef);
                    }
                    CFRelease(minRef);
                }

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

                out_arr[count].pid       = pid;
                out_arr[count].cgid      = (uint32_t)cgid;
                out_arr[count].title     = title;
                out_arr[count].app_name  = strdup(appName);
                out_arr[count].minimized = (bool)isMin;
                out_arr[count].hidden    = (bool)appHidden;
                count++;
                axEmittedForApp++;
            }
            CFRelease(windowsRef);
        }
        CFRelease(appAX);

        // Fallback: AX sometimes returns an empty window list for hidden
        // apps (Cmd+H'd background processes whose accessibility tree
        // hasn't been instantiated yet). Discover their windows via
        // CGWindowListCopyWindowInfo so they still show up in the
        // switcher; raise will unhide the app and re-resolve via AX.
        if (axEmittedForApp == 0 && appHidden && count < max) {
            CFArrayRef cgWindows = CGWindowListCopyWindowInfo(
                kCGWindowListOptionAll, kCGNullWindowID);
            if (cgWindows != NULL) {
                CFIndex cgN = CFArrayGetCount(cgWindows);
                for (CFIndex i = 0; i < cgN && count < max; i++) {
                    CFDictionaryRef d =
                        (CFDictionaryRef)CFArrayGetValueAtIndex(cgWindows, i);
                    if (d == NULL) continue;

                    CFNumberRef ownerPidRef =
                        (CFNumberRef)CFDictionaryGetValue(d, kCGWindowOwnerPID);
                    if (ownerPidRef == NULL) continue;
                    pid_t ownerPid = 0;
                    CFNumberGetValue(ownerPidRef, kCFNumberIntType, &ownerPid);
                    if (ownerPid != pid) continue;

                    CFNumberRef layerRef =
                        (CFNumberRef)CFDictionaryGetValue(d, kCGWindowLayer);
                    int layer = -1;
                    if (layerRef != NULL) {
                        CFNumberGetValue(layerRef, kCFNumberIntType, &layer);
                    }
                    if (layer != 0) continue;

                    CFDictionaryRef boundsDict =
                        (CFDictionaryRef)CFDictionaryGetValue(d, kCGWindowBounds);
                    if (boundsDict == NULL) continue;
                    CGRect bounds = CGRectZero;
                    if (!CGRectMakeWithDictionaryRepresentation(boundsDict, &bounds)) {
                        continue;
                    }
                    if (bounds.size.width < 64.0 || bounds.size.height < 64.0) {
                        continue;
                    }

                    CFNumberRef numRef =
                        (CFNumberRef)CFDictionaryGetValue(d, kCGWindowNumber);
                    if (numRef == NULL) continue;
                    uint32_t cgid = 0;
                    CFNumberGetValue(numRef, kCFNumberSInt32Type, &cgid);
                    if (cgid == 0) continue;

                    char *title = NULL;
                    CFStringRef nameRef =
                        (CFStringRef)CFDictionaryGetValue(d, kCGWindowName);
                    if (nameRef != NULL) {
                        title = dup_cfstring_utf8(nameRef);
                    }
                    if (title == NULL) title = strdup("");

                    out_arr[count].pid       = pid;
                    out_arr[count].cgid      = cgid;
                    out_arr[count].title     = title;
                    out_arr[count].app_name  = strdup(appName);
                    out_arr[count].minimized = false;
                    out_arr[count].hidden    = true;
                    count++;
                }
                CFRelease(cgWindows);
            }
        }

        free(appName);
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
// process pid, un-hides the owning app and un-minimises the window if
// needed, then raises it via AX and activates the NSRunningApplication.
// Returns true on success.
//
// Hidden apps: NSRunningApplication.unhide makes the app's windows
// reappear in the windowserver, but the AX tree may not be populated
// for the just-unhid app on the first poll, so we retry a few times
// with a short sleep between attempts.
//
// Minimised windows: AXMinimized must be set to false BEFORE kAXRaise
// or the raise is a no-op (the window stays in the Dock).
bool gwim_raise_window(pid_t pid, uint32_t cgid) {
    NSRunningApplication *app =
        [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
    if (app != nil && [app isHidden]) {
        [app unhide];
    }

    AXUIElementRef appAX = AXUIElementCreateApplication(pid);
    if (appAX == NULL) {
        if (app != nil) {
            [app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
        }
        return false;
    }

    bool raised = false;
    // Retry the AX lookup a few times — if we just called -unhide, the
    // AX tree can take a few tens of ms to settle. Total budget ~150ms.
    for (int attempt = 0; attempt < 4 && !raised; attempt++) {
        CFTypeRef windowsRef = NULL;
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
                    AXUIElementSetAttributeValue(win, kAXMinimizedAttribute,
                                                  kCFBooleanFalse);
                    AXUIElementPerformAction(win, kAXRaiseAction);
                    AXUIElementSetAttributeValue(win, kAXMainAttribute,
                                                  kCFBooleanTrue);
                    raised = true;
                    break;
                }
            }
            CFRelease(windowsRef);
        }
        if (!raised) {
            usleep(50 * 1000); // 50ms
        }
    }
    CFRelease(appAX);

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
