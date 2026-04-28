//go:build darwin

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

	if !macos.RequestAccessibilityPermission(true) {
		log.Printf("Accessibility permission not yet granted. " +
			"GWiM will keep running; grant it in " +
			"System Settings → Privacy & Security → Accessibility, then restart.")
	}

	wmgr := macos.NewWindowManager()
	hkmgr := macos.NewHotkeyManager()

	eng, err := engine.New(engine.Config{
		WindowManager: wmgr,
		HotkeyManager: hkmgr,
		Actions:       engine.DefaultActions(),
		Shortcuts:     engine.DefaultShortcuts(),
		Blocklist:     engine.DefaultBlocklist(),
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

	tray := ui.New(eng, Version)
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
