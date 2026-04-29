//go:build windows

package windows

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

// kernel32 binding for the process-image-path helpers. We bind directly
// rather than going through golang.org/x/sys/windows so the package only
// has the one third-party dep on x/sys (consumed exclusively for the
// registry helper used by launchatlogin.go).
var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")

	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
)

// PROCESS_QUERY_LIMITED_INFORMATION is the access right introduced in
// Vista that lets us read the image path of any process — including
// elevated ones — without needing PROCESS_QUERY_INFORMATION (which
// requires the same elevation level as the target).
const processQueryLimitedInformation = 0x1000

// GetActiveAppIdentifier returns the basename of the executable owning
// the foreground window (e.g. "chrome.exe"). This is the Windows
// analogue of macOS's bundle identifier — the engine surfaces it in the
// tray for diagnostics.
func (m *winWindowManager) GetActiveAppIdentifier() (string, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", nil
	}

	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", nil
	}

	hProc, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if hProc == 0 {
		return "", nil
	}
	defer procCloseHandle.Call(hProc)

	var buf [1024]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(
		hProc,
		0, // dwFlags = 0 → Win32 path format
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return "", nil
	}
	return filepath.Base(syscall.UTF16ToString(buf[:size])), nil
}
