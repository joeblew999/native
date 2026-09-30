package input

import (
	"errors"
	"runtime"
	"testing"
)

// TestUnknownValues pins the input validation that runs before anything is
// posted. It posts nothing: every call here fails first.
func TestUnknownValues(t *testing.T) {
	if runtime.GOOS != "darwin" {
		err := Target(1).KeyTap(KeyA)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err %v, want ErrUnsupported", err)
		}
		t.Skip("no backend on", runtime.GOOS)
	}
	if !Trusted() {
		t.Skip("no Accessibility permission: validation runs after the permission check")
	}
	// PID 1 is launchd; nothing reaches it, the calls fail validation first.
	err := Target(1).KeyTap(Key(9999))
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("KeyTap(9999): %v, want ErrUnknownKey", err)
	}
	err = Target(1).KeyTap(KeyA, Modifier(99))
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("KeyTap(mod 99): %v, want ErrUnknownKey", err)
	}
}
