//go:build darwin

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/nealhardesty/gwim/internal/engine"
	"github.com/nealhardesty/gwim/internal/platform/macos"
	"github.com/nealhardesty/gwim/internal/ui"
)

// startApp wires the macOS-specific window/hotkey backends into the engine
// and hands the main goroutine to the systray run loop.
//
// Order of operations:
//
//  1. Lock the OS thread — systray's NSApplication run loop *must* run on
//     the main thread on macOS or hotkey events arrive on the wrong queue.
//  2. Request accessibility permission (with prompt). Without it, no AX
//     calls succeed and every action would fail silently.
//  3. Build engine and tray.
//  4. Start systray; it owns the goroutine until Quit is selected.
func startApp() error {
	runtime.LockOSThread()

	// Prompt once at startup. Subsequent re-checks (every PollInterval
	// and after action errors) are silent — see Config.AccessibilityCheck
	// below. The tray surfaces the live state visibly.
	if !macos.RequestAccessibilityPermission(true) {
		log.Printf("Accessibility permission not yet granted. " +
			"GWiM will keep running; grant it in " +
			"System Settings → Privacy & Security → Accessibility. " +
			"The menu-bar status will update automatically once granted.")
	}

	wmgr := macos.NewWindowManager()
	hkmgr := macos.NewHotkeyManager()

	toggle := engine.DefaultToggleHotkey()
	eng, err := engine.New(engine.Config{
		WindowManager: wmgr,
		HotkeyManager: hkmgr,
		Actions:       engine.DefaultActions(),
		Shortcuts:     engine.DefaultShortcuts(),
		ToggleHotkey:  toggle,
		Blocklist:     engine.DefaultBlocklist(),
		// NON-prompting check — the engine polls this every tick to keep
		// the tray's "Accessibility: granted/denied" row honest.
		AccessibilityCheck: func() bool { return macos.RequestAccessibilityPermission(false) },
	})
	if err != nil {
		return fmt.Errorf("engine init: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("signal received, shutting down")
		cancel()
		eng.Stop()
	}()

	tray := ui.New(eng, Version, toggle.Format())
	// Wire the "click to fix" affordance on the AX status row.
	tray.OpenAccessibilitySettings = openAccessibilityPane
	tray.LaunchAtLogin = &ui.LaunchAtLoginHooks{
		Supported: macos.LaunchAtLoginSupported,
		IsOn:      macos.LaunchAtLoginEnabled,
		Set:       macos.SetLaunchAtLogin,
	}
	tray.Run(func() {
		if err := eng.Run(ctx); err != nil {
			log.Printf("engine run: %v", err)
		}
	}, func() {
		cancel()
		eng.Stop()
	})
	return nil
}

// openAccessibilityPane jumps directly to System Settings →
// Privacy & Security → Accessibility. Uses the well-known x-apple
// preferences URL scheme — works on macOS 10.10 and later.
func openAccessibilityPane() {
	const url = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	if err := exec.Command("open", url).Start(); err != nil {
		log.Printf("open accessibility settings: %v", err)
	}
}
