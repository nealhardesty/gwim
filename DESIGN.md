
# Product Requirements Document (PRD): "GWiM" (Golang Window Manager)

Github Repo will be at https://github.com/nealhardesty/gwim

## 1. Project Overview
**GWiM** is a lightweight, compiled background application written in Golang that provides keyboard-driven window management, replacing a legacy Hammerspoon (Lua) setup. The application allows users to resize, tile, move, and throw windows between screens using specific hotkey combinations. 

To ensure seamless workflows when using remote desktops or similar apps,
GWiM supports **manual suspension**: hotkeys can be turned off from the
menu bar or with **`Ctrl+Alt+X`** so keystrokes reach the foreground app
unchanged. It also includes a **System Tray / Menu Bar UI** for toggling and
interactive shortcut execution.

The initial target is macOS, utilizing direct OS API calls via `cgo` (Accessibility API, Carbon/Cocoa), with an architecture designed to easily support Windows (`user32.dll` / `WinAPI`) in a fast-follow update.

---

## 2. Goals & Non-Goals
**Goals:**
* Achieve 1:1 functional parity with the original legacy Hammerspoon script.
* Use Golang as the primary language with minimal external dependencies.
* Provide optional manual suspension so users can yield hotkey control when using remote desktop or similar clients (menu bar or **`Ctrl+Alt+X`**).
* Provide a Menu Bar (macOS) / System Tray (Windows) interface with an interactive shortcut reference.
* Structure the codebase to abstract platform-specific window manipulation, hotkey registration, and UI rendering.

**Non-Goals:**
* Creating a traditional, heavy desktop application UI. The visual footprint is strictly limited to the menu bar/system tray and its associated dropdowns/popups.
* Create an in-app configuration system.  Configuration is fine to be hard coded into the app, but easy to modify via code in one place.
* Automatic window tiling (like Yabai or i3). This remains purely a manual, hotkey-driven window manager.
* Linux/X11/Wayland support (initially out of scope, though the architecture should not strictly block it).

---

## 3. Functional Requirements

### 3.1. Manual Suspension
The user must be able to suspend global hotkey hooks so keystrokes reach the foreground application (for example a remote-desktop client). Suspension is **explicit**: via the menu bar **Suspend / Activate** item or the persistent **`Ctrl+Alt+X`** shortcut. While suspended, regular shortcuts are not dispatched; the tray **Shortcuts** submenu still runs actions on click (explicit intent). There is **no** automatic suspension based on the foreground application's bundle identifier.

### 3.2. Menu Bar / System Tray UI
The application must run with an icon in the macOS Menu Bar (and eventually the Windows System Tray).
* **Toggle State:** A menu item to manually Suspend/Activate the application.
* **Interactive Reference:** A menu item that opens a sub-menu listing all available shortcuts.
    * These listed shortcuts must be **clickable**. Clicking a shortcut in the UI should execute the corresponding window management action on the currently active window, exactly as if the hotkey was pressed.
* **Quit:** A clean exit option.
* It needs a simple icon for both platforms

### 3.3. Window Tiling & Snapping (Hotkeys)
**Modifiers:** `Ctrl + Alt`
* **Halves:**
    * `Left` or `h`: Snap to Left Half.
    * `Right` or `l`: Snap to Right Half.
    * `Up`: Snap to Top Half.
    * `Down`: Snap to Bottom Half.
* **Quarters:**
    * `u`: Snap to Top-Left Quarter.
    * `i`: Snap to Top-Right Quarter.
    * `j`: Snap to Bottom-Left Quarter.
    * `k`: Snap to Bottom-Right Quarter.
* **Thirds:**
    * `1`: Snap to Left Third.
    * `2`: Snap to Middle Third.
    * `3`: Snap to Right Third.
* **Fourths:**
    * `4`: Snap to First (Leftmost) Fourth.
    * `5`: Snap to Second Fourth.
    * `6`: Snap to Third Fourth.
    * `7`: Snap to Fourth (Rightmost) Fourth.
* **Horizontal Strips (Bottom 1/4th height):**
    * `m`: Bottom horizontal strip (Full width).
    * `n`: Bottom-Left horizontal strip (Left half width).
    * `,` (comma): Bottom-Right horizontal strip (Right half width).
* **Full Screen:**
    * `Return`: Expand frame to fill the screen (Frame-based, does not trigger native spaces).
    * `f`: Toggle Native macOS Fullscreen.

### 3.4. Relative Window Moving
**Modifiers:** `Ctrl + Alt + Shift`
* `h` or `Left`: Move Left by 100px.
* `j` or `Up`: Move Up by 100px *(Y-100)*.
* `k` or `Down`: Move Down by 100px *(Y+100)*.
* `l` or `Right`: Move Right by 100px.

### 3.5. Relative Window Resizing
**Modifiers:** `Ctrl + Alt + Shift + Cmd` (On Windows, Cmd = Win key)
* `h` or `Left`: Decrease Width by 100px.
* `j` or `Up`: Decrease Height by 100px.
* `k` or `Down`: Increase Height by 100px.
* `l` or `Right`: Increase Width by 100px.

### 3.6. Screen Jumping (Multi-Monitor)
**Modifiers:** `Ctrl + Alt + Cmd`
* `Left` or `h`: Move window to the West (Previous) Screen.
* `Right` or `l`: Move window to the East (Next) Screen.
* `Up` or `k`: Move window to the North Screen (if present).
* `Down` or `j`: Move window to the South Screen (if present).

### 3.7. Alt-Tab Window Switcher
A keyboard-driven, MRU-ordered switcher across **individual windows** (not
applications). Full requirements live in [`ALTTAB.md`](ALTTAB.md).

* **Triggers:** `Option+Tab` (forward) / `Option+Shift+Tab` (backward) —
  bound via the same `engine.Shortcut` table that drives the rest of the
  hotkeys.
* **Overlay:** one borderless `NSWindow` per `NSScreen`, each centred in
  that display's **visible** (working) area and scaled so the panel uses up
  to **90%** of that area's width and height (uniform scale per monitor,
  preserving layout), so thumbnails stay readable on large and mixed-size
  setups; every panel shows the same MRU grid and selection. Each slot shows a live window thumbnail
  (captured via ScreenCaptureKit's `SCScreenshotManager`, macOS
  14+) with the application icon as a small badge in the bottom-right
  corner; falls back to a centred app icon when capture fails (Screen
  Recording denied, occluded window, owning process gone, etc.). The
  selected window's full title is shown in a strip below the grid.
* **Coverage:** every standard window of every regular app is enumerated,
  **including minimised windows and the windows of hidden (Cmd+H) apps**.
  These slots are drawn at reduced opacity so they're recognisable at a
  glance; committing to one un-hides the owning app and un-minimises
  the window before raising. For hidden apps where AX returns no
  windows yet, the enumerator falls back to
  `CGWindowListCopyWindowInfo` so the windows still appear in the
  switcher.
* **Event handling:** while the overlay is open, GWiM installs a
  `CGEventTap` at `kCGSessionEventTap` / `kCGHeadInsertEventTap` that
  intercepts Tab, Shift+Tab, Esc, Return, and the Option flag-changed
  event. The tap is removed on commit/cancel so it never sees user input
  outside an active switch.
* **MRU bookkeeping:** `internal/altswitch/Stash` keeps the per-window
  MRU history; the currently focused window pins to position 0 so the
  default highlight is the second entry, matching familiar Alt-Tab.
* **Tray integration:** the same actions appear in a "Window Switcher"
  category in the Shortcuts submenu. Clicking opens the overlay in
  *modal mode* (`Return` commits, `Esc` cancels) since no Option key is
  held when invoked from the menu.

---

## 4. Technical Architecture

### 4.1. Core Interfaces (`internal/wm/`)
Generic interfaces abstracting the OS layer, now including application identification and state management.

```go
type Rect struct {
    X, Y, W, H float64
}

type Window interface {
    GetFrame() Rect
    SetFrame(Rect) error
    ToggleFullScreen() error
    MoveToScreen(direction string) error 
}

type WindowManager interface {
    GetActiveWindow() (Window, error)
    GetScreenFrame(Window) (Rect, error)
    GetActiveAppIdentifier() (string, error) // Returns Bundle ID (Mac) or EXE name (Win)
}

type HotkeyManager interface {
    Register(modifiers []string, key string, handler func()) error
    Listen() error
    SetSuspended(bool) // Enables manual toggle from the tray
}
```

### 4.2. UI Layer (`internal/ui/`)
* **Library Recommendation:** Use a cross-platform tray library like `github.com/getlantern/systray` to manage the lifecycle of the menu bar/tray icon.
* **Threading Constraint:** On macOS, the UI loop *must* run on the main thread. The application entry point (`main.go`) must initialize the systray, and dispatch the background `WindowManager` and `HotkeyManager` logic to goroutines.

### 4.3. macOS Implementation (`internal/platform/macos/`)
* **Window Management:** Implement via macOS Accessibility API (`AXUIElement`) and `CGO` using `<ApplicationServices/ApplicationServices.h>`.
* **Active App Detection:** Use `NSWorkspace sharedWorkspace frontmostApplication bundleIdentifier` via CGO/Objective-C to fetch the active app identifier for status display in the tray.
* **Hotkey Management:** Use `NSEvent addGlobalMonitorForEventsMatchingMask` or Carbon's `RegisterEventHotKey`.
* **Alt-Tab Switcher** (`altswitch_native.m` + `altswitch.go`): borderless
  `NSWindow` overlay drawn from a custom `NSView`; layout scales to fit
  within 90% of the primary screen `visibleFrame` while keeping a fixed
  grid design. `CGEventTap` for Tab/Shift+Tab/Esc/Return/Option-release;
  cross-process AX enumeration via `AXUIElementCreateApplication` +
  `kAXWindowsAttribute`; window identity via `_AXUIElementGetWindow`
  (long-stable private API for CGWindowID lookup); raise via
  `kAXRaiseAction` + `[NSRunningApplication activateWithOptions:]`. Live
  thumbnails are captured via `SCScreenshotManager.captureImageWithFilter:`
  (macOS 14+), using `SCShareableContent` once per overlay open to map
  CGWindowID → `SCWindow`. Screen Recording permission is probed via
  `CGPreflightScreenCaptureAccess` and triggered via
  `CGRequestScreenCaptureAccess`.
* **Permissions:** Requires **Accessibility** (System Settings → Privacy
  & Security → Accessibility). Live window thumbnails additionally
  require **Screen Recording**; switcher degrades gracefully to icons
  alone when Screen Recording is denied.

### 4.4. Windows Extensibility (`internal/platform/windows/`)
*(To be scaffolded)*
* **Window Management & Detection:** Use `x/sys/windows` for `GetForegroundWindow()`, `GetWindowThreadProcessId()`, and `QueryFullProcessImageNameW()` (executable identification for parity with macOS foreground display).
* **Hotkeys:** Use `RegisterHotKey` from `user32.dll`.

### 4.5. Business Logic Layer (`internal/engine/`)
* **Action Dispatcher:** The engine maps shortcuts to functions. Crucially, these functions must be accessible both by the `HotkeyManager` (when a key is pressed) and the `TrayUI` (when a menu item is clicked).
* **Middleware/Interceptor:** Before executing any window manipulation triggered by a keyboard shortcut, the engine evaluates whether the app is manually suspended (menu bar or **`Ctrl+Alt+X`**). If suspended, the shortcut is ignored; tray clicks still invoke **`Execute`** (explicit user intent).

---

## 5. Security & Lifecycle Considerations
* **macOS App Bundle:** The binary must be compiled into a `.app` bundle.
* **Info.plist Configuration:** `<key>LSUIElement</key><true/>` is required so it runs as an agent without an icon in the main Dock, only appearing in the Menu Bar.
* **State Management:** The "Suspended/Active" state should ideally be stored in memory during runtime. If persistence across reboots is desired, it can be written to a simple local JSON config.

---

## 6. Suggested Bootstrapping Steps for the AI
1.  **Project Initialization:** Create `go.mod`, fetch `getlantern/systray`, and set up the directory structure (`cmd/`, `internal/wm/`, `internal/platform/`, `internal/engine/`, `internal/ui/`).
2.  **Define Interfaces:** Write the interfaces in `internal/wm/`.
3.  **Scaffold macOS CGO Wrappers:** * Window manipulation (`AXUIElement`).
    * Active application bundle ID retrieval (`NSWorkspace`).
    * Global hotkey registration.
4.  **Build the Logic Engine:** Map the hotkeys to layout calculations. Implement the `ActionDispatcher` so both hotkeys and UI clicks can trigger the exact same functions.
5.  **Wire suspension:** Ensure manual suspend unregisters regular hotkeys with the OS while keeping the **`Ctrl+Alt+X`** toggle registered (`RegisterPersistent`).
6.  **Wire the Tray UI:** Set up the `systray` main loop, add the Suspend toggle, and generate the interactive dropdown/popup list of shortcuts.
7.  **Provide Build Scripts:** Write a `Makefile` that compiles the Go binary, generates the `.app` bundle structure, writes the `Info.plist`, and injects the required macOS icons (`.icns`).
