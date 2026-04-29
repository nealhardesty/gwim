//go:build windows

package windows

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is the standard userland startup key on Windows. Values
// here are launched on first interactive login; no admin rights needed
// (HKCU is per-user). HKLM\...\Run would apply to every user but
// requires elevation, which GWiM intentionally avoids.
const (
	runKeyPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runKeyValue = "GWiM"
)

// LaunchAtLoginSupported reports whether Open at Login can be used.
// Always true on Windows — every supported version of Windows has the
// Run key.
func LaunchAtLoginSupported() bool { return true }

// LaunchAtLoginEnabled reports whether GWiM is registered to start at
// user login. We require not just the value's presence but that it
// points to the currently running executable, so a stale entry from a
// previous install location reads as "not enabled".
func LaunchAtLoginEnabled() bool {
	want, err := os.Executable()
	if err != nil {
		return false
	}
	got, err := readRunValue()
	if err != nil {
		return false
	}
	return strings.EqualFold(stripQuotes(got), want)
}

// SetLaunchAtLogin registers (true) or unregisters (false) GWiM as a
// startup item. Values are quoted so paths containing spaces (e.g.
// `C:\Program Files\GWiM\gwim.exe`) launch correctly.
func SetLaunchAtLogin(enable bool) error {
	if !enable {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if err != nil {
			// Missing parent key means there's nothing to delete.
			if errors.Is(err, registry.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("open Run key: %w", err)
		}
		defer k.Close()
		if err := k.DeleteValue(runKeyValue); err != nil &&
			!errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("delete Run value: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create Run key: %w", err)
	}
	defer k.Close()

	// Wrap in literal double quotes so Windows's command-line parser
	// treats the whole path as one argument, even if it contains spaces
	// (e.g. "C:\Program Files\GWiM\gwim.exe"). %q is wrong here — it
	// would escape the backslashes via Go-syntax rules.
	value := `"` + exe + `"`
	if err := k.SetStringValue(runKeyValue, value); err != nil {
		return fmt.Errorf("write Run value: %w", err)
	}
	return nil
}

// readRunValue returns the raw GWiM Run value (with its quotes intact)
// or an error if the key/value is missing.
func readRunValue() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runKeyValue)
	if err != nil {
		return "", err
	}
	return v, nil
}

// stripQuotes removes a single layer of surrounding double quotes, the
// shape Windows expects for a Run value containing a path with spaces.
func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
