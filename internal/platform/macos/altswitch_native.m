// altswitch_native.m — macOS native implementation of the Alt-Tab window
// switcher (per ALTTAB.md / DESIGN.md §3.7).
//
// Compiled by cgo as Objective-C alongside the Go files in this package
// because @interface / @implementation cannot live in a cgo preamble (cgo
// preambles are translated as plain C, not ObjC). The Go side declares
// extern symbols and calls into them; the //export'd Go callback
// gwimAltswitchEvent receives event-tap notifications.
//
// Five responsibilities:
//
//  1. Borderless NSWindow overlay(s) on the main thread — one mirrored
//     panel per NSScreen so the switcher is visible on every display.
//     The panel renders a wrapping grid of slots GROUPED BY macOS Space:
//     a small label header per group ("D1·S2", "Sticky", …) with thin
//     dividers between groups. Each slot shows a live window thumbnail
//     (when Screen Recording is granted) plus the application icon as a
//     small badge; falls back to the icon alone when capture is
//     unavailable.
//
//  2. CGEventTap installed only while the overlay is open. Captures Tab,
//     Shift+Tab, Esc, Return, and the Option flag-changed event so the
//     Go controller can advance / commit / cancel. Overlay windows accept
//     mouse events; clicking a slot commits like Return.
//
//  3. CGWindowList-driven enumeration covering every Space (the
//     primary source of truth — AX is unreliable for windows on other
//     Spaces, especially native-fullscreen Spaces). AX is consulted as
//     an enrichment layer for subrole / minimised / title.
//
//  4. CGS Spaces metadata — private but long-stable APIs used by every
//     comparable open-source switcher (Yabai, Hammerspoon, AltTab.app)
//     to discover which Space each window lives on, build per-display
//     ordering, and detect sticky (all-Spaces) windows.
//
//  5. Screen Recording permission probe + request, mirroring the
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

// Forward declaration of the Go callbacks. cgo synthesises these symbols
// from the //export directives in altswitch.go.
extern void gwimAltswitchEvent(int kind, int keycode, int optionDown, int shiftDown);
extern void gwimAltswitchSlotClicked(int idx);

// Private API: maps an AXUIElementRef back to its CGWindowID. Stable
// since Snow Leopard; widely used by open-source window managers.
extern AXError _AXUIElementGetWindow(AXUIElementRef element, CGWindowID *out);

// =====================================================================
// CGS Spaces — private CoreGraphics APIs.
// =====================================================================
//
// These four entry points have been stable since macOS 10.7 and are used
// by Yabai, Hammerspoon, AltTab.app, Rectangle, etc. They are weak-linked
// from CoreGraphics (already in our framework set via Cocoa). If a future
// macOS removes them, gwim_enumerate_windows degrades gracefully: every
// window lands in a single "Spaces" group and the rest of the switcher
// keeps working.
extern int        CGSMainConnectionID(void);
extern CFArrayRef CGSCopySpacesForWindows(int cid, int mask, CFArrayRef windowIDs);
extern CFArrayRef CGSCopyManagedDisplaySpaces(int cid);
extern uint64_t   CGSGetActiveSpace(int cid);

// kCGSAllSpacesMask = 0x7 is the canonical value (USER | OTHERS | CURRENT)
// observed across Yabai / AltTab / Hammerspoon. Using 0xFFFFFFFF works
// too but adds nothing.
static const int kGwimAllSpacesMask = 0x7;

// Type code returned by macOS for native-fullscreen Spaces in the
// "type" field of CGSCopyManagedDisplaySpaces entries. Used only for
// the human-readable group label.
static const int kGwimSpaceTypeFullscreen = 4;

// =====================================================================
// gwim_window_entry — must match the typedef in altswitch.go's cgo
// preamble byte-for-byte. Adding fields requires updating BOTH places.
// =====================================================================

typedef struct {
    pid_t    pid;
    uint32_t cgid;
    char    *title;        // strdup'd; freed by gwim_free_window_entries
    char    *app_name;     // strdup'd; freed by gwim_free_window_entries
    bool     minimized;    // window is currently minimized to the Dock
    bool     hidden;       // owning app is hidden via Cmd+H / NSRunningApplication.hide
    uint64_t space_id;     // CGS Space identifier (0 if unknown)
    int32_t  group_rank;   // visual group ordering: smaller = appears earlier
    bool     sticky;       // window appears on every Space (CanJoinAllSpaces)
    char    *space_label;  // strdup'd group label, e.g. "D1\u00b7S2", "Sticky", "FS"
} gwim_window_entry;

// =====================================================================
// Slot geometry — shared between layout and drawing so hit testing and
// paint stay in lock-step. Slots are 3:2 to match typical window aspect.
// =====================================================================

static const CGFloat kGwimSlotW         = 144.0;
static const CGFloat kGwimSlotH         = 96.0;
static const CGFloat kGwimSlotPad       = 18.0;
static const CGFloat kGwimTitleH        = 30.0;
static const CGFloat kGwimGroupHeaderH  = 22.0;
static const CGFloat kGwimGroupGapV     = 12.0;
static const int     kGwimGroupMaxCols  = 6;
static const CGFloat kGwimOverlayMaxScreenFraction = 0.9;

// =====================================================================
// Overlay view
// =====================================================================

@interface GWIMOverlayView : NSView
@property (nonatomic, strong) NSArray *icons;        // NSImage* or NSNull* (always app icons)
@property (nonatomic, strong) NSArray *thumbnails;   // NSImage* or NSNull* (one per slot)
@property (nonatomic, strong) NSArray *titles;       // NSString*
@property (nonatomic, strong) NSArray *appNames;     // NSString*
/// Boxed BOOL per slot — YES means draw the icon/thumbnail at reduced
/// opacity to indicate the window is minimised or its app is hidden.
@property (nonatomic, strong) NSArray *dimmed;
/// Per-slot precomputed NSRect (NSValue-boxed) used for both painting
/// and hit testing. Re-derived in gwim_overlay_show every time the slot
/// list changes.
@property (nonatomic, strong) NSArray *slotRects;
/// Group label NSString for each slot (same string for every slot in a
/// group). Drawing detects boundaries by string equality with the
/// previous slot's label.
@property (nonatomic, strong) NSArray *slotGroupLabels;
/// Per-slot frame for the group header. NSValue-boxed NSRect, NSZeroRect
/// when the slot is not the first in its group.
@property (nonatomic, strong) NSArray *groupHeaderRects;
/// Per-slot frame for the divider line drawn AFTER the group containing
/// this slot. NSZeroRect except on the last slot of each group (omitted
/// for the final group so we don't draw a trailing divider).
@property (nonatomic, strong) NSArray *groupDividerRects;
@property (nonatomic) NSInteger selected;
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

    NSInteger n = (NSInteger)[self.slotRects count];
    if (n == 0) return;

    CGFloat ringOutset    = 10.0 * sc;
    CGFloat ringCorner    = 14.0 * sc;
    CGFloat ringLine      = MAX(1.0, 3.0 * sc);
    CGFloat thumbInset    = 4.0 * sc;
    CGFloat thumbCorner   = 6.0 * sc;
    CGFloat badge         = 28.0 * sc;
    CGFloat badgeMargin   = 6.0 * sc;
    CGFloat iconSize      = 64.0 * sc;
    CGFloat placeholderR  = 10.0 * sc;
    CGFloat titleFont     = MAX(11.0, round(14.0 * sc));
    CGFloat headerFont    = MAX(10.0, round(12.0 * sc));
    CGFloat dividerAlpha  = 0.18;

    // Group dividers — drawn underneath the slots so they don't interfere
    // with the selection ring on the highlighted slot.
    for (NSInteger i = 0; i < n; i++) {
        NSRect divider = [(NSValue *)self.groupDividerRects[i] rectValue];
        if (NSIsEmptyRect(divider)) continue;
        [[NSColor colorWithCalibratedWhite:1.0 alpha:dividerAlpha] setFill];
        NSRectFill(divider);
    }

    // Group headers — small label per group, drawn in muted white.
    NSDictionary *headerAttrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:headerFont
                                               weight:NSFontWeightSemibold],
        NSForegroundColorAttributeName:
            [NSColor colorWithCalibratedWhite:1.0 alpha:0.75],
    };
    for (NSInteger i = 0; i < n; i++) {
        NSRect headerRect = [(NSValue *)self.groupHeaderRects[i] rectValue];
        if (NSIsEmptyRect(headerRect)) continue;
        NSString *label = (i < (NSInteger)self.slotGroupLabels.count)
            ? (NSString *)self.slotGroupLabels[i] : @"";
        if (label.length == 0) continue;
        [label drawAtPoint:headerRect.origin withAttributes:headerAttrs];
    }

    for (NSInteger i = 0; i < n; i++) {
        NSRect slotRect = [(NSValue *)self.slotRects[i] rectValue];
        if (NSIsEmptyRect(slotRect)) continue;

        const CGFloat slotW = slotRect.size.width;
        const CGFloat slotH = slotRect.size.height;

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
            NSRect drawTarget = aspectFitRect([thumb size], thumbBounds);

            // Soft rounded clip + subtle background so letterbox bars are
            // less obtrusive when the thumbnail isn't 3:2.
            [[NSColor colorWithCalibratedWhite:0.0 alpha:0.55] setFill];
            NSBezierPath *frame = [NSBezierPath bezierPathWithRoundedRect:thumbBounds
                                                                  xRadius:thumbCorner yRadius:thumbCorner];
            [frame fill];

            [NSGraphicsContext saveGraphicsState];
            [frame addClip];
            [thumb drawInRect:drawTarget
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

    // Title for currently selected window.
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
        CGFloat pad = kGwimSlotPad * sc;
        if (tx < pad) tx = pad;
        CGFloat ty = pad / 2.0;
        [full drawAtPoint:NSMakePoint(tx, ty) withAttributes:attrs];
    }
}

- (void)mouseUp:(NSEvent *)event {
    NSInteger n = (NSInteger)[self.slotRects count];
    if (n == 0) return;
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    for (NSInteger i = 0; i < n; i++) {
        NSRect r = [(NSValue *)self.slotRects[i] rectValue];
        if (!NSIsEmptyRect(r) && NSPointInRect(p, r)) {
            gwimAltswitchSlotClicked((int)i);
            return;
        }
    }
}

@end

// =====================================================================
// Overlay layout — group-aware. Walks the per-slot group label list,
// emitting a header-row + slot rows for each contiguous group, and a
// thin divider between groups.
// =====================================================================

typedef struct {
    CGFloat width;       // intrinsic width (max group row width)
    CGFloat height;      // intrinsic height (sum of all group blocks)
    NSArray *slotRects;          // NSValue (NSRect) per slot
    NSArray *headerRects;        // NSValue (NSRect) per slot — only set on group's first slot
    NSArray *dividerRects;       // NSValue (NSRect) per slot — only set on group's last slot (except final)
} gwim_layout_result;

static gwim_layout_result gwim_compute_layout(NSArray *slotGroupLabels, CGFloat scale) {
    NSInteger n = (NSInteger)[slotGroupLabels count];
    gwim_layout_result out;
    NSMutableArray *slotRects    = [NSMutableArray arrayWithCapacity:n];
    NSMutableArray *headerRects  = [NSMutableArray arrayWithCapacity:n];
    NSMutableArray *dividerRects = [NSMutableArray arrayWithCapacity:n];
    for (NSInteger i = 0; i < n; i++) {
        [slotRects    addObject:[NSValue valueWithRect:NSZeroRect]];
        [headerRects  addObject:[NSValue valueWithRect:NSZeroRect]];
        [dividerRects addObject:[NSValue valueWithRect:NSZeroRect]];
    }
    out.slotRects = slotRects;
    out.headerRects = headerRects;
    out.dividerRects = dividerRects;
    out.width = 0;
    out.height = 0;
    if (n == 0) return out;

    CGFloat sc       = scale;
    if (sc < 1e-6) sc = 1.0;
    CGFloat slotW    = kGwimSlotW * sc;
    CGFloat slotH    = kGwimSlotH * sc;
    CGFloat pad      = kGwimSlotPad * sc;
    CGFloat headerH  = kGwimGroupHeaderH * sc;
    CGFloat groupGap = kGwimGroupGapV * sc;
    CGFloat outerPad = pad;
    CGFloat titleH   = kGwimTitleH * sc;

    // Find group boundaries by scanning the per-slot labels for changes.
    NSMutableArray<NSNumber *> *groupStarts = [NSMutableArray array];
    NSString *prev = nil;
    for (NSInteger i = 0; i < n; i++) {
        NSString *label = (NSString *)slotGroupLabels[i];
        if (i == 0 || ![label isEqualToString:prev]) {
            [groupStarts addObject:@(i)];
        }
        prev = label;
    }

    NSInteger ng = (NSInteger)groupStarts.count;

    // First pass: compute intrinsic width = widest single row across all
    // groups. Each group wraps at kGwimGroupMaxCols.
    CGFloat maxRowSlots = 0;
    for (NSInteger g = 0; g < ng; g++) {
        NSInteger start = groupStarts[g].integerValue;
        NSInteger end   = (g + 1 < ng) ? groupStarts[g + 1].integerValue : n;
        NSInteger size  = end - start;
        if (size <= 0) continue;
        NSInteger rowSlots = size < kGwimGroupMaxCols ? size : kGwimGroupMaxCols;
        if (rowSlots > maxRowSlots) maxRowSlots = rowSlots;
    }
    if (maxRowSlots == 0) maxRowSlots = 1;
    CGFloat contentW = maxRowSlots * slotW + (maxRowSlots - 1) * pad;
    CGFloat panelW   = contentW + 2 * outerPad;
    if (panelW < 320) panelW = 320;

    // Second pass: lay out each group from the top of the panel down,
    // recording per-slot rects. Y origin is unflipped (NSView default), so
    // we work top-to-bottom by tracking the current "top" Y and decrementing.
    //
    // We don't yet know panelH; build the layout in "panel-local top-anchored"
    // coordinates with y=0 at the top edge, then translate at the end.
    CGFloat top = 0;            // distance below panel top
    top += outerPad;            // small breathing room above first header

    for (NSInteger g = 0; g < ng; g++) {
        NSInteger start = groupStarts[g].integerValue;
        NSInteger end   = (g + 1 < ng) ? groupStarts[g + 1].integerValue : n;
        NSInteger size  = end - start;
        if (size <= 0) continue;

        // Grid is centred horizontally inside the panel; gridLeft is the
        // left edge of every row in this group.
        CGFloat gridLeft = outerPad;
        NSRect headerR = NSMakeRect(gridLeft, top, contentW, headerH);
        headerRects[start] = [NSValue valueWithRect:headerR];
        top += headerH;

        NSInteger cols = size < kGwimGroupMaxCols ? size : kGwimGroupMaxCols;
        NSInteger rows = (size + cols - 1) / cols;

        for (NSInteger row = 0; row < rows; row++) {
            NSInteger rowStart = row * cols;
            NSInteger rowSize  = MIN(cols, size - rowStart);
            // Left-align row within the group's content rect (consistent
            // start for partially-filled rows).
            for (NSInteger c = 0; c < rowSize; c++) {
                NSInteger slotIdx = start + rowStart + c;
                CGFloat x = gridLeft + c * (slotW + pad);
                NSRect r = NSMakeRect(x, top, slotW, slotH);
                slotRects[slotIdx] = [NSValue valueWithRect:r];
            }
            top += slotH;
            if (row + 1 < rows) top += pad;
        }

        // Divider after this group (skip after the last group).
        if (g + 1 < ng) {
            top += groupGap / 2.0;
            NSRect divR = NSMakeRect(gridLeft, top, contentW, MAX(1.0, 1.0 * sc));
            dividerRects[end - 1] = [NSValue valueWithRect:divR];
            top += MAX(1.0, 1.0 * sc);
            top += groupGap / 2.0;
        }
    }

    top += outerPad;            // top padding above title strip
    top += titleH;              // bottom title strip
    top += outerPad / 2.0;      // bottom margin

    CGFloat panelH = top;
    if (panelH < 180) panelH = 180;

    // Now flip Y from "top-anchored" to NSView's bottom-origin coordinates.
    // For each rect: newY = panelH - (oldY + rect.height).
    NSMutableArray *flipSlots    = [NSMutableArray arrayWithCapacity:n];
    NSMutableArray *flipHeaders  = [NSMutableArray arrayWithCapacity:n];
    NSMutableArray *flipDividers = [NSMutableArray arrayWithCapacity:n];
    for (NSInteger i = 0; i < n; i++) {
        NSRect r = [slotRects[i] rectValue];
        NSRect h = [headerRects[i] rectValue];
        NSRect d = [dividerRects[i] rectValue];
        if (!NSIsEmptyRect(r)) r.origin.y = panelH - r.origin.y - r.size.height;
        if (!NSIsEmptyRect(h)) h.origin.y = panelH - h.origin.y - h.size.height;
        if (!NSIsEmptyRect(d)) d.origin.y = panelH - d.origin.y - d.size.height;
        [flipSlots    addObject:[NSValue valueWithRect:r]];
        [flipHeaders  addObject:[NSValue valueWithRect:h]];
        [flipDividers addObject:[NSValue valueWithRect:d]];
    }

    out.width        = panelW;
    out.height       = panelH;
    out.slotRects    = flipSlots;
    out.headerRects  = flipHeaders;
    out.dividerRects = flipDividers;
    return out;
}

// =====================================================================
// Overlay window plumbing
// =====================================================================

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
// capture, etc.).
//
// CGWindowListCreateImage was the obvious fit but Apple obsoleted it in
// the macOS 15 SDK — the symbol is no longer linkable. SCK is the only
// forward-compatible option.
//
// SCK's capture API is async, so we bridge to sync via dispatch
// semaphores. This function is called from a goroutine (showOverlay
// runs off the Carbon hotkey dispatch goroutine), never from the main
// thread, so the blocking is harmless. Per-window timeout is 1 s; the
// up-front getShareableContent call gets 2 s.
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
//   pids[i]                 — owning process pid (used for app icon lookup)
//   cgids[i]                — CGWindowID (used for thumbnail capture; 0 to skip)
//   dimmed_flags[i]         — non-zero ⇒ slot is rendered at reduced opacity
//                             (window is minimised or its app is hidden)
//   titles_and_apps[i*2]    — window title (UTF-8 C string, may be empty)
//   titles_and_apps[i*2+1]  — application localised name (UTF-8 C string)
//   group_labels[i]         — group label (UTF-8 C string, repeated within a group)
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
                       const char **group_labels,
                       int count,
                       int selected) {
    if (count <= 0) return;

    NSMutableArray *icons    = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *titleArr = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *appArr   = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *dimArr   = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *labelArr = [NSMutableArray arrayWithCapacity:count];

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

        const char *g = (group_labels != NULL) ? group_labels[i] : "";
        [labelArr addObject:(g ? [NSString stringWithUTF8String:g] : @"")];
    }

    // Batch-capture thumbnails. Returns NSNull* placeholders for windows
    // we couldn't snapshot (denied permission, occluded, gone), so the
    // overlay falls back to the app icon for those slots.
    NSArray *thumbnails = (cgids != NULL)
        ? gwim_capture_thumbnails(cgids, count)
        : [NSArray array];

    // Compute layout once at unit scale to learn intrinsic dims; the
    // per-screen scaling happens inside the dispatch block below where we
    // know each screen's visibleFrame.
    gwim_layout_result baseLayout = gwim_compute_layout(labelArr, 1.0);
    CGFloat intrinsicW = baseLayout.width;
    CGFloat intrinsicH = baseLayout.height;
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
            if (s > 1.0) s = 1.0;  // never upscale past native geometry
            if (s < 0.4) s = 0.4;  // floor: ridiculous overlays still legible
            gwim_layout_result laid = gwim_compute_layout(labelArr, s);
            CGFloat width  = laid.width;
            CGFloat height = laid.height;
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
            view.slotRects = laid.slotRects;
            view.slotGroupLabels = labelArr;
            view.groupHeaderRects = laid.headerRects;
            view.groupDividerRects = laid.dividerRects;
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

static CFMachPortRef     gTap        = NULL;
static CFRunLoopSourceRef gTapSource = NULL;

static CGEventRef gwim_event_tap_callback(CGEventTapProxy proxy,
                                          CGEventType type,
                                          CGEventRef event,
                                          void *refcon) {
    (void)proxy; (void)refcon;

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
// CGS Spaces helpers
// =====================================================================

// gwim_space_for_window returns the primary Space ID that owns this
// window. If the window appears on >1 Space, *out_sticky is set to true.
// Returns 0 if the lookup fails.
static uint64_t gwim_space_for_window(int cid, uint32_t cgid, bool *out_sticky) {
    if (out_sticky) *out_sticky = false;
    if (cgid == 0) return 0;
    CGWindowID wid = (CGWindowID)cgid;
    CFNumberRef num = CFNumberCreate(NULL, kCFNumberSInt32Type, &wid);
    if (num == NULL) return 0;
    CFArrayRef widArr = CFArrayCreate(NULL, (const void **)&num, 1, &kCFTypeArrayCallBacks);
    CFRelease(num);
    if (widArr == NULL) return 0;

    CFArrayRef spaces = CGSCopySpacesForWindows(cid, kGwimAllSpacesMask, widArr);
    CFRelease(widArr);
    if (spaces == NULL) return 0;

    uint64_t result = 0;
    CFIndex n = CFArrayGetCount(spaces);
    if (n > 0) {
        CFNumberRef sn = (CFNumberRef)CFArrayGetValueAtIndex(spaces, 0);
        if (sn != NULL && CFGetTypeID(sn) == CFNumberGetTypeID()) {
            CFNumberGetValue(sn, kCFNumberSInt64Type, &result);
        }
    }
    if (n > 1 && out_sticky) *out_sticky = true;
    CFRelease(spaces);
    return result;
}

// SpaceMeta describes one (display, space) tuple discovered via
// CGSCopyManagedDisplaySpaces. Used for ranking groups and labelling
// them in the overlay.
typedef struct {
    uint64_t space_id;
    int      display_index;
    int      space_order;       // 0-based position within the display
    int      space_type;        // CGSSpaceType: 0=user, 4=fullscreen
    bool     is_current;        // currently visible on this display
    bool     is_focused;        // matches the focused window's space
} gwim_space_meta;

// gwim_collect_space_metadata walks CGSCopyManagedDisplaySpaces and
// builds a map from space_id -> gwim_space_meta. Out params are caller-
// owned and freed via free(). Returns the number of spaces discovered.
//
// also returns the focused space id via out_focused_space (0 if none).
static int gwim_collect_space_metadata(int cid,
                                        uint64_t focused_space_id,
                                        bool single_display_only,
                                        gwim_space_meta **out_metas) {
    *out_metas = NULL;
    CFArrayRef displays = CGSCopyManagedDisplaySpaces(cid);
    if (displays == NULL) return 0;

    CFIndex displayCount = CFArrayGetCount(displays);

    // First pass: total space count.
    int total = 0;
    for (CFIndex d = 0; d < displayCount; d++) {
        CFDictionaryRef dd = (CFDictionaryRef)CFArrayGetValueAtIndex(displays, d);
        if (dd == NULL) continue;
        CFArrayRef sp = (CFArrayRef)CFDictionaryGetValue(dd, CFSTR("Spaces"));
        if (sp == NULL) continue;
        total += (int)CFArrayGetCount(sp);
    }
    if (total == 0) {
        CFRelease(displays);
        return 0;
    }

    gwim_space_meta *metas = (gwim_space_meta *)calloc(total, sizeof(gwim_space_meta));
    if (metas == NULL) {
        CFRelease(displays);
        return 0;
    }

    int idx = 0;
    for (CFIndex d = 0; d < displayCount; d++) {
        CFDictionaryRef dd = (CFDictionaryRef)CFArrayGetValueAtIndex(displays, d);
        if (dd == NULL) continue;
        CFArrayRef sp = (CFArrayRef)CFDictionaryGetValue(dd, CFSTR("Spaces"));
        if (sp == NULL) continue;

        // "Current Space" is the one currently visible on this display.
        uint64_t currentSpaceID = 0;
        CFDictionaryRef curr = (CFDictionaryRef)CFDictionaryGetValue(dd, CFSTR("Current Space"));
        if (curr != NULL) {
            CFNumberRef sid = (CFNumberRef)CFDictionaryGetValue(curr, CFSTR("ManagedSpaceID"));
            if (sid == NULL) sid = (CFNumberRef)CFDictionaryGetValue(curr, CFSTR("id64"));
            if (sid != NULL) CFNumberGetValue(sid, kCFNumberSInt64Type, &currentSpaceID);
        }

        CFIndex spc = CFArrayGetCount(sp);
        for (CFIndex s = 0; s < spc; s++) {
            CFDictionaryRef spd = (CFDictionaryRef)CFArrayGetValueAtIndex(sp, s);
            if (spd == NULL) continue;
            CFNumberRef sid = (CFNumberRef)CFDictionaryGetValue(spd, CFSTR("ManagedSpaceID"));
            if (sid == NULL) sid = (CFNumberRef)CFDictionaryGetValue(spd, CFSTR("id64"));
            uint64_t sval = 0;
            if (sid != NULL) CFNumberGetValue(sid, kCFNumberSInt64Type, &sval);

            int stype = 0;
            CFNumberRef tn = (CFNumberRef)CFDictionaryGetValue(spd, CFSTR("type"));
            if (tn != NULL) CFNumberGetValue(tn, kCFNumberIntType, &stype);

            metas[idx].space_id      = sval;
            metas[idx].display_index = (int)d;
            metas[idx].space_order   = (int)s;
            metas[idx].space_type    = stype;
            metas[idx].is_current    = (sval == currentSpaceID && sval != 0);
            metas[idx].is_focused    = (focused_space_id != 0 && sval == focused_space_id);
            idx++;
        }
    }

    CFRelease(displays);

    // If we only want to surface single-display labels (suppress D{n}
    // prefix when there's just one display), the caller can detect that
    // by inspecting the metas. We don't use the flag here yet but keep
    // it for symmetry with the public surface.
    (void)single_display_only;

    *out_metas = metas;
    return idx;
}

static const gwim_space_meta *gwim_lookup_space(const gwim_space_meta *metas,
                                                  int meta_count,
                                                  uint64_t space_id) {
    if (metas == NULL || space_id == 0) return NULL;
    for (int i = 0; i < meta_count; i++) {
        if (metas[i].space_id == space_id) return &metas[i];
    }
    return NULL;
}

// gwim_format_space_label builds the human-readable label for a group,
// honouring the single-display shorthand. Returns a strdup'd string.
static char *gwim_format_space_label(const gwim_space_meta *meta,
                                       int display_count,
                                       bool sticky) {
    char buf[64];
    if (sticky) {
        return strdup("Sticky");
    }
    if (meta == NULL) {
        return strdup("Spaces");
    }
    const char *suffix = (meta->space_type == kGwimSpaceTypeFullscreen) ? " · FS" : "";
    if (display_count <= 1) {
        snprintf(buf, sizeof(buf), "Space %d%s%s",
                 meta->space_order + 1,
                 meta->is_current ? " · current" : "",
                 suffix);
    } else {
        snprintf(buf, sizeof(buf), "D%d · S%d%s%s",
                 meta->display_index + 1,
                 meta->space_order + 1,
                 meta->is_current ? " · current" : "",
                 suffix);
    }
    return strdup(buf);
}

// gwim_compute_group_rank assigns the visual order rank for a group.
// Lower = earlier in the overlay. The focused space sits at rank 0,
// followed by other current-on-display spaces, then non-current spaces
// in (display, space) order, then sticky last.
static int32_t gwim_compute_group_rank(const gwim_space_meta *meta,
                                        bool sticky,
                                        int display_count) {
    if (sticky) return INT32_MAX;
    if (meta == NULL) return INT32_MAX - 1;
    if (meta->is_focused) return 0;
    int displayBlock = 1 + meta->display_index;
    // Reserve top-of-display slots for "current on display" spaces.
    int withinDisplay = meta->is_current ? 0 : (1 + meta->space_order);
    return (int32_t)(displayBlock * 1000 + withinDisplay);
    (void)display_count;
}

// =====================================================================
// Window enumeration
// =====================================================================

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

// AX info for one window — title (if any), minimised flag, standard-window
// subrole flag. Used to enrich CGWindowList entries.
typedef struct {
    bool     have_ax;
    bool     is_standard;
    bool     minimized;
    char    *title;        // strdup'd or NULL
} gwim_ax_info;

// Build a per-pid map of (CGWindowID -> gwim_ax_info) for the supplied
// pid by walking AXUIElementCopyAttributeValue(kAXWindowsAttribute).
// Caller owns the returned NSMutableDictionary; values are NSValue-boxed
// pointers to heap gwim_ax_info structs, which the caller must free.
static NSMutableDictionary *gwim_build_ax_map_for_pid(pid_t pid) {
    NSMutableDictionary *map = [NSMutableDictionary dictionary];
    AXUIElementRef appAX = AXUIElementCreateApplication(pid);
    if (appAX == NULL) return map;

    CFTypeRef windowsRef = NULL;
    AXError err = AXUIElementCopyAttributeValue(appAX, kAXWindowsAttribute,
                                                  &windowsRef);
    if (err == kAXErrorSuccess && windowsRef != NULL) {
        CFArrayRef windows = (CFArrayRef)windowsRef;
        CFIndex n = CFArrayGetCount(windows);
        for (CFIndex i = 0; i < n; i++) {
            AXUIElementRef win =
                (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);

            CGWindowID cgid = 0;
            if (_AXUIElementGetWindow(win, &cgid) != kAXErrorSuccess || cgid == 0) {
                continue;
            }

            gwim_ax_info *info = (gwim_ax_info *)calloc(1, sizeof(gwim_ax_info));
            if (info == NULL) continue;
            info->have_ax = true;

            CFTypeRef subroleRef = NULL;
            if (AXUIElementCopyAttributeValue(win, kAXSubroleAttribute, &subroleRef)
                == kAXErrorSuccess && subroleRef != NULL) {
                if (CFGetTypeID(subroleRef) == CFStringGetTypeID() &&
                    CFStringCompare((CFStringRef)subroleRef,
                                    kAXStandardWindowSubrole, 0)
                    == kCFCompareEqualTo) {
                    info->is_standard = true;
                }
                CFRelease(subroleRef);
            }

            CFTypeRef minRef = NULL;
            if (AXUIElementCopyAttributeValue(win, kAXMinimizedAttribute, &minRef)
                == kAXErrorSuccess && minRef != NULL) {
                if (CFGetTypeID(minRef) == CFBooleanGetTypeID()) {
                    info->minimized = CFBooleanGetValue((CFBooleanRef)minRef);
                }
                CFRelease(minRef);
            }

            CFTypeRef titleRef = NULL;
            if (AXUIElementCopyAttributeValue(win, kAXTitleAttribute, &titleRef)
                == kAXErrorSuccess && titleRef != NULL) {
                if (CFGetTypeID(titleRef) == CFStringGetTypeID()) {
                    info->title = dup_cfstring_utf8((CFStringRef)titleRef);
                }
                CFRelease(titleRef);
            }

            map[@((uint32_t)cgid)] = [NSValue valueWithPointer:info];
        }
        CFRelease(windowsRef);
    }
    CFRelease(appAX);
    return map;
}

static void gwim_free_ax_map(NSDictionary *map) {
    for (NSValue *v in [map allValues]) {
        gwim_ax_info *info = (gwim_ax_info *)[v pointerValue];
        if (info == NULL) continue;
        if (info->title) free(info->title);
        free(info);
    }
}

// gwim_enumerate_windows fills out_arr with up to max entries describing
// every "standard" window across every macOS Space. The primary source
// is CGWindowListCopyWindowInfo (so windows on other Spaces — including
// native-fullscreen Spaces — are always included). AX is consulted as
// an enrichment layer for subrole filtering, minimised state, and
// titles.
//
// Caller MUST call gwim_free_window_entries to free the per-entry
// strdup'd strings.
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
    if (out_arr == NULL || max <= 0) return 0;

    pid_t selfPid = getpid();

    // Discover the focused window via the system-wide AX element so we
    // always know the pin target even if enumeration is later truncated.
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

    // Build a pid -> NSRunningApplication cache once; we'll consult it
    // for activation policy, hidden state, and localised name.
    NSMutableDictionary<NSNumber *, NSRunningApplication *> *appByPid =
        [NSMutableDictionary dictionary];
    NSArray<NSRunningApplication *> *apps =
        [[NSWorkspace sharedWorkspace] runningApplications];
    for (NSRunningApplication *app in apps) {
        if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;
        pid_t pid = [app processIdentifier];
        if (pid <= 0) continue;
        appByPid[@(pid)] = app;
    }

    // Lazy AX enrichment per pid.
    NSMutableDictionary<NSNumber *, NSDictionary *> *axByPid =
        [NSMutableDictionary dictionary];

    // Primary enumeration: every window across every Space, including
    // off-screen ones. Layer 0 + sane bounds + regular owner is the
    // standard recipe used by every comparable open-source switcher.
    CFArrayRef cgWindows = CGWindowListCopyWindowInfo(
        kCGWindowListOptionAll | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (cgWindows == NULL) return 0;

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
        if (ownerPid <= 0) continue;
        if (ownerPid == selfPid) continue;            // skip our own overlay

        NSRunningApplication *app = appByPid[@(ownerPid)];
        if (app == nil) continue;                     // non-regular activation policy

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
        if (!CGRectMakeWithDictionaryRepresentation(boundsDict, &bounds)) continue;
        if (bounds.size.width < 64.0 || bounds.size.height < 64.0) continue;

        CFNumberRef numRef =
            (CFNumberRef)CFDictionaryGetValue(d, kCGWindowNumber);
        if (numRef == NULL) continue;
        uint32_t cgid = 0;
        CFNumberGetValue(numRef, kCFNumberSInt32Type, &cgid);
        if (cgid == 0) continue;

        // Ensure we have AX info for this pid (lazy build).
        NSDictionary *axMap = axByPid[@(ownerPid)];
        if (axMap == nil) {
            axMap = gwim_build_ax_map_for_pid(ownerPid);
            axByPid[@(ownerPid)] = axMap;
        }

        gwim_ax_info *axInfo = NULL;
        NSValue *axBox = axMap[@(cgid)];
        if (axBox != nil) axInfo = (gwim_ax_info *)[axBox pointerValue];

        // Subrole filter: when AX has the window, require AXStandardWindow.
        // When AX is silent (common for windows on other Spaces), trust
        // the layer/bounds/regular-owner filter we already passed.
        if (axInfo != NULL && axInfo->have_ax && !axInfo->is_standard) {
            continue;
        }

        // Title: AX preferred (more accurate, no Screen Recording cost),
        // CGWindowList's kCGWindowName as fallback (often empty without
        // Screen Recording permission).
        char *title = NULL;
        if (axInfo != NULL && axInfo->title != NULL) {
            title = strdup(axInfo->title);
        } else {
            CFStringRef nameRef =
                (CFStringRef)CFDictionaryGetValue(d, kCGWindowName);
            if (nameRef != NULL) {
                title = dup_cfstring_utf8(nameRef);
            }
        }
        if (title == NULL) title = strdup("");

        char *appName = NULL;
        NSString *localized = [app localizedName];
        if (localized != nil) {
            const char *u = [localized UTF8String];
            if (u != NULL) appName = strdup(u);
        }
        if (appName == NULL) appName = strdup("");

        bool minimized = (axInfo != NULL && axInfo->minimized);
        bool hidden    = (bool)[app isHidden];

        out_arr[count].pid         = ownerPid;
        out_arr[count].cgid        = cgid;
        out_arr[count].title       = title;
        out_arr[count].app_name    = appName;
        out_arr[count].minimized   = minimized;
        out_arr[count].hidden      = hidden;
        out_arr[count].space_id    = 0;        // filled below
        out_arr[count].group_rank  = INT32_MAX;
        out_arr[count].sticky      = false;
        out_arr[count].space_label = NULL;
        count++;
    }
    CFRelease(cgWindows);

    // Free the AX-info heap allocations now that we've copied everything
    // we needed out of them.
    for (NSDictionary *m in [axByPid allValues]) {
        gwim_free_ax_map(m);
    }

    if (count == 0) return 0;

    // Spaces metadata — cheap to call once.
    int cid = CGSMainConnectionID();

    // Per-window space lookup. cgid==0 cases were filtered above so this
    // always has a real id.
    for (int i = 0; i < count; i++) {
        bool sticky = false;
        out_arr[i].space_id = gwim_space_for_window(cid, out_arr[i].cgid, &sticky);
        out_arr[i].sticky   = sticky;
    }

    // Resolve the focused space id from the focused window we discovered
    // earlier (or fall back to CGSGetActiveSpace if AX failed).
    uint64_t focusedSpaceID = 0;
    if (out_focused_cgid != NULL && *out_focused_cgid != 0) {
        bool ignored = false;
        focusedSpaceID = gwim_space_for_window(cid, *out_focused_cgid, &ignored);
    }
    if (focusedSpaceID == 0) {
        focusedSpaceID = CGSGetActiveSpace(cid);
    }

    gwim_space_meta *metas = NULL;
    int metaCount = gwim_collect_space_metadata(cid, focusedSpaceID, false, &metas);

    // How many displays are involved? Influences label format.
    int displayCount = 0;
    for (int i = 0; i < metaCount; i++) {
        if (metas[i].display_index + 1 > displayCount) {
            displayCount = metas[i].display_index + 1;
        }
    }
    if (displayCount == 0) displayCount = 1;

    // Build per-window group_rank + label.
    for (int i = 0; i < count; i++) {
        const gwim_space_meta *m =
            gwim_lookup_space(metas, metaCount, out_arr[i].space_id);
        out_arr[i].group_rank  =
            gwim_compute_group_rank(m, out_arr[i].sticky, displayCount);
        out_arr[i].space_label =
            gwim_format_space_label(m, displayCount, out_arr[i].sticky);
    }

    if (metas != NULL) free(metas);
    return count;
}

void gwim_free_window_entries(gwim_window_entry *arr, int count) {
    if (arr == NULL) return;
    for (int i = 0; i < count; i++) {
        if (arr[i].title)       free(arr[i].title);
        if (arr[i].app_name)    free(arr[i].app_name);
        if (arr[i].space_label) free(arr[i].space_label);
        arr[i].title       = NULL;
        arr[i].app_name    = NULL;
        arr[i].space_label = NULL;
    }
}

// =====================================================================
// Window raise
// =====================================================================
//
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
