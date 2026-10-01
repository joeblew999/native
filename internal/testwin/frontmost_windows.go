//go:build hwtest || hwtest_global

package testwin

import (
	"fmt"
	"syscall"
	"testing"
)

var procGetForegroundWindow = syscall.NewLazyDLL("user32.dll").NewProc("GetForegroundWindow")

// Frontmost names the foreground window (its HWND), the thing these tests
// promise never to change. It may be 0 on a CI runner nobody has touched.
func Frontmost(t *testing.T) string {
	t.Helper()
	h, _, _ := procGetForegroundWindow.Call()
	return fmt.Sprintf("%#x", h)
}
