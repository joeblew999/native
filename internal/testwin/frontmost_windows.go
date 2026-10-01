//go:build hwtest || hwtest_global

package testwin

import (
	"fmt"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procGetForegroundWindow      = syscall.NewLazyDLL("user32.dll").NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowThreadProcessId")
)

// ForegroundPID is the process that owns the foreground window, 0 for none.
func ForegroundPID() int {
	h, _, _ := procGetForegroundWindow.Call()
	if h == 0 {
		return 0
	}
	var pid uint32
	_, _, _ = procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	return int(pid)
}

// Frontmost names the foreground window (its HWND), the thing these tests
// promise never to change. It may be 0 on a CI runner nobody has touched.
func Frontmost(t *testing.T) string {
	t.Helper()
	h, _, _ := procGetForegroundWindow.Call()
	return fmt.Sprintf("%#x", h)
}
