// Package screen captures windows and the display, cgo-free.
//
// CaptureWindow reads one window's own pixels, whether or not it is covered by
// other windows or sitting in the background, so a program can watch another
// application without bringing it forward:
//
//	img, err := screen.CaptureWindow(windowID)
//
// Capture reads a rectangle of the main display as the user sees it.
//
// Both need the Screen Recording permission (CaptureAllowed reports it);
// without it they return ErrNotAllowed. This package never prompts for it.
//
// Images come back at the display's pixel resolution, so on a Retina display a
// 400x300-point window is an 800x600 image. Colours are converted to sRGB.
//
// Implemented on macOS through ScreenCaptureKit (macOS 14 or later).
// Elsewhere every call returns ErrUnsupported.
package screen

import (
	"errors"
	"image"
)

var (
	// ErrUnsupported is returned on a platform with no capture backend.
	ErrUnsupported = errors.New("screen: not supported on this platform")
	// ErrNotAllowed is returned when the process lacks the Screen Recording
	// permission. Grant it in System Settings, Privacy & Security, Screen &
	// System Audio Recording; this package never prompts for it.
	ErrNotAllowed = errors.New("screen: screen recording permission not granted")
	// ErrNoWindow is returned by CaptureWindow for an ID that names no window.
	ErrNoWindow = errors.New("screen: no such window")
)

// CaptureWindow returns the current content of the window with the given ID
// (a CGWindowID on macOS), frame and title bar included, without its shadow.
// The window may be covered or in the background.
func CaptureWindow(windowID uint32) (*image.RGBA, error) { return captureWindow(windowID) }

// Capture returns rectangle r of the main display, in points from its top-left
// corner, as currently shown. r is clipped to the display.
func Capture(r image.Rectangle) (*image.RGBA, error) {
	if r.Empty() {
		return nil, errors.New("screen: empty rectangle")
	}
	return capture(r)
}

// Size returns the main display's size in points.
func Size() (w, h int, err error) { return size() }

// CaptureAllowed reports whether this process holds the Screen Recording
// permission. It never prompts. On a platform with no backend it returns false.
func CaptureAllowed() bool { return captureAllowed() }
