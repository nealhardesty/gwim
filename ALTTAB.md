# Product Requirements Document (PRD): Alt-Tab Window Switcher (GWiM add-on)

This document supplements [`DESIGN.md`](DESIGN.md) for a future **window switcher** module inside **GWiM** (Golang Window Manager).

---

## 1. Executive Summary

**Project name:** Alt-Tab window switcher module for GWiM

**Objective:** Add a lightweight, zero-configuration “Alt-Tab” switcher to GWiM so users can move keyboard focus among **individual windows** (not just applications), using a fixed **Option+Tab** chord on macOS—the usual stand-in for “Alt+Tab” on Windows and Linux.

---

## 2. Problem Statement

macOS **Command+Tab** cycles **applications**. Users coming from Windows or Linux often want **per-window** switching (MRU across windows) without Mission Control or the mouse.

---

## 3. Scope

**In scope**

- Keyboard-driven window switching.
- Fixed shortcuts: **Option+Tab** (forward) and **Option+Shift+Tab** (backward).
- Overlay with window thumbnails (and app icons for recognition).
- Most-recently-used (MRU) ordering.

**Out of scope**

- User-configurable shortcuts.
- Theming or layout customization beyond a single fixed overlay design.
- Closing, minimizing, or quitting windows from the switcher.
- Drag-and-drop or file targets.
- App blocklists or filtering inside the switcher.
- Dedicated CLI for the switcher.

---

## 4. Functional requirements

### 4.1 Core switching mechanics

- **REQ-4.1.1 Triggers:** Listen for **Option+Tab** to open or advance the switcher forward; **Option+Shift+Tab** to move backward. While the overlay is open, **Option stays held** and repeated **Tab** (or **Shift+Tab**) presses move the highlight—the same chord pattern as typical Alt-Tab on other platforms (see §7).
- **REQ-4.1.2 MRU ordering:** Windows are ordered by focus history: most recently focused first, then second, and so on. The **currently focused window is included** in the list (typically first).
- **REQ-4.1.3 Default selection:** On opening the overlay, the **highlight starts on the window that will receive focus on the first forward advance**—i.e. the **second** entry in the MRU list when at least two windows exist—matching familiar Alt-Tab behavior. With only one window, behavior is defined by implementation (no-op or single row).
- **REQ-4.1.4 Commit:** Releasing **Option** dismisses the overlay and activates the **currently highlighted** window.

### 4.2 Visual interface

- **REQ-4.2.1 Layout:** One fixed-design overlay **per connected display** (mirrored content), each centred in that screen’s **visible** (working) area and scaled independently so the panel fits that monitor’s working region (see `DESIGN.md` §3.7 for the scale cap).
- **REQ-4.2.2 Content:** Show each candidate window with a **live thumbnail** where permitted, plus its **application icon** for quick scanning.

### 4.3 Menu bar integration

- **REQ-4.3.1:** List the switcher in the menu-bar **Shortcuts** submenu alongside other GWiM actions (see [`DESIGN.md`](DESIGN.md) §3.2). Shortcut labels and any click-to-run items must follow the same patterns as existing tray shortcut rows.

---

## 5. Non-functional requirements

- **NFR-5.1 Performance:** Background overhead should stay **small** in line with GWiM’s menu-bar agent model (concrete targets such as idle RAM and CPU can be set once the module is profiled).
- **NFR-5.2 OS compatibility:** Same macOS baseline and architecture targets as the shipping GWiM binary (see project build settings and [`README.md`](README.md)).
- **NFR-5.3 Privacy:** Local-only operation: **no** telemetry and **no** network use for this feature.

---

## 6. Dependencies and permissions

macOS APIs required:

1. **Accessibility:** Needed to register global shortcuts (Option combinations), enumerate windows, and raise the chosen window—same class of permission as the rest of GWiM.
2. **Screen Recording:** Needed for **live thumbnails** of other windows. If the user denies it, degrade gracefully (e.g. **static app icons** or placeholder art instead of live previews).

**Menu-bar parity:** Surface permission status in the tray in line with existing patterns: e.g. an **Accessibility** row that reflects grant state and can open **System Settings**, and a comparable **Screen Recording** row with the same semantics so users can fix denials without hunting through panes.

---

## 7. UX / user flow

1. User holds **Option** and presses **Tab** to open the overlay.
2. Overlay appears; selection reflects MRU rules in §4.1 (default highlight per REQ-4.1.3).
3. With **Option still held**, user presses **Tab** again to move forward along the list, or **Shift+Tab** to move backward.
4. User releases **Option**.
5. Overlay closes and the highlighted window becomes focused.

---

## 8.  Documentation
- When implemented, be sure to update @README.md, @DESIGN.md and the @CHANGELOG.md appropriately to reflect the new functionality.
