//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/nealhardesty/gwim/internal/engine"
	"github.com/nealhardesty/gwim/internal/platform/windows"
	"github.com/nealhardesty/gwim/internal/ui"
)

// startApp wires the Windows-specific window/hotkey backends into the
// engine and hands the main goroutine to the systray run loop.
//
// Order of operations mirrors main_darwin.go:
//
//  1. Lock the OS thread — getlantern/systray's Windows backend pumps
//     its own message loop on this thread, so we keep it stable.
//  2. Build engine and tray (no AX / Screen Recording probes — Windows
//     has no equivalent permission gate).
//  3. Start systray; it owns the goroutine until Quit is selected.
//
// The Alt-Tab switcher is intentionally skipped on Windows: the OS
// already provides a fully featured Alt+Tab and the user explicitly
// asked us not to duplicate it.
func startApp() error {
	runtime.LockOSThread()

	wmgr := windows.NewWindowManager()
	hkmgr := windows.NewHotkeyManager()

	toggle := engine.DefaultToggleHotkey()
	actions := engine.DefaultActions()
	shortcuts := engine.DefaultShortcuts()

	eng, err := engine.New(engine.Config{
		WindowManager: wmgr,
		HotkeyManager: hkmgr,
		Actions:       actions,
		Shortcuts:     shortcuts,
		ToggleHotkey:  toggle,
		// No AccessibilityCheck / ScreenRecordingCheck on Windows —
		// the corresponding tray rows hide themselves automatically.
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
	tray.LaunchAtLogin = &ui.LaunchAtLoginHooks{
		Supported: windows.LaunchAtLoginSupported,
		IsOn:      windows.LaunchAtLoginEnabled,
		Set:       windows.SetLaunchAtLogin,
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
