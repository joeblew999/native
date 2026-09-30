package screen

import (
	"errors"
	"image"
	"runtime"
	"testing"
)

func TestSize(t *testing.T) {
	w, h, err := Size()
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	// A headless CI runner still has a (virtual) main display on macOS.
	if w <= 0 || h <= 0 {
		t.Fatalf("Size() = %d x %d", w, h)
	}
}

func TestCaptureEmptyRect(t *testing.T) {
	_, err := Capture(image.Rectangle{})
	if err == nil {
		t.Fatal("Capture of an empty rectangle succeeded")
	}
}

func TestUnsupported(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin has a backend")
	}
	_, err := CaptureWindow(1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err %v, want ErrUnsupported", err)
	}
}
