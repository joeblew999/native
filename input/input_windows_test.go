package input

import (
	"errors"
	"testing"
	"unsafe"
)

// TestStructLayout pins the structs passed to user32 by pointer to their C
// sizes (INPUT is 40 bytes on 64-bit Windows, 28 on 386). A wrong size makes
// SendInput fail with ERROR_INVALID_PARAMETER, or GetGUIThreadInfo read past
// the struct.
func TestStructLayout(t *testing.T) {
	want := map[string][2]uintptr{ // 64-bit, 32-bit
		"mouseInput":    {40, 28},
		"keybdInput":    {40, 28},
		"guiThreadInfo": {72, 48},
	}
	got := map[string]uintptr{
		"mouseInput":    unsafe.Sizeof(mouseInput{}),
		"keybdInput":    unsafe.Sizeof(keybdInput{}),
		"guiThreadInfo": unsafe.Sizeof(guiThreadInfo{}),
	}
	i := 0
	if unsafe.Sizeof(uintptr(0)) == 4 {
		i = 1
	}
	for name, w := range want {
		if got[name] != w[i] {
			t.Errorf("sizeof %s = %d, want %d", name, got[name], w[i])
		}
	}
}

// TestNoWindow: every App method needs a window on Windows; the System
// process (PID 4) has none, so nothing is posted.
func TestNoWindow(t *testing.T) {
	app := Target(4)
	for name, err := range map[string]error{
		"KeyTap":     app.KeyTap(KeyA),
		"TypeString": app.TypeString("x"),
		"Click":      app.Click(1, 1, Left),
		"Scroll":     app.Scroll(0, 1),
	} {
		if !errors.Is(err, ErrNoWindow) {
			t.Errorf("%s: %v, want ErrNoWindow", name, err)
		}
	}
}
