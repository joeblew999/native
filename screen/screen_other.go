//go:build !darwin && !windows

// Fallback for platforms without a capture backend yet. Keeps the module
// building for every GOOS; every call fails with ErrUnsupported.

package screen

import "image"

func captureWindow(_ uint32) (*image.RGBA, error) { return nil, ErrUnsupported }

func capture(_ image.Rectangle) (*image.RGBA, error) { return nil, ErrUnsupported }

func size() (int, int, error) { return 0, 0, ErrUnsupported }

func captureAllowed() bool { return false }
