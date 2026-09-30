# screen

Capture a window or the display, cgo-free. The screenshot part of
[robotgo](https://github.com/go-vgo/robotgo), without the C toolchain, plus
the thing robotgo cannot do: capture **one window**, even when it is covered or
in the background, without bringing it forward.

```go
import "github.com/crgimenes/native/screen"

img, err := screen.CaptureWindow(windowID) // *image.RGBA
```

## API

| Func | Description |
| --- | --- |
| `CaptureWindow(windowID uint32) (*image.RGBA, error)` | One window's own pixels (frame and title bar, no shadow), covered or not. `windowID` is a `CGWindowID` on macOS. |
| `Capture(r image.Rectangle) (*image.RGBA, error)` | Rectangle `r` of the main display, in points from its top-left, as the user sees it. Clipped to the display. |
| `Size() (w, h int, err error)` | Main display size in points. |
| `CaptureAllowed() bool` | Whether the process holds the Screen Recording permission. Never prompts. |

Images are at pixel resolution: on a Retina display a 400x300-point window
comes back 800x600. Colours are converted to sRGB, and `image.RGBA`'s
premultiplied layout is what CoreGraphics draws, so there is no per-pixel
conversion in Go.

Errors: `ErrUnsupported` (no backend), `ErrNotAllowed` (no Screen Recording
permission), `ErrNoWindow` (no window with that ID). No native types cross the
API.

## Platforms

| OS | Backend | Status |
| --- | --- | --- |
| macOS 14+ | ScreenCaptureKit: `SCShareableContent`, `SCContentFilter`, `SCScreenshotManager` | `CaptureWindow`: ✅ tested on hardware (arm64); `Capture`: code only, see below |
| Windows | — | ⬜ `ErrUnsupported` (separate PR) |
| Linux | — | ⬜ `ErrUnsupported` (separate PR) |

`CGWindowListCreateImage`, the old one-call API, is obsoleted from macOS 15;
ScreenCaptureKit is the replacement, and the only one that captures a single
covered window.

## Permission

Capturing needs **Screen Recording** (System Settings → Privacy & Security →
Screen & System Audio Recording) for the process or the terminal that launched
it. Without it this package returns `ErrNotAllowed`; it never prompts and never
works around the permission. `Size` needs no permission.

## Measured

2026-09-30, macOS 27.0.1 (26A434), Apple silicon, `go test -tags hwtest
./screen`, against [`examples/testwin`](../examples/testwin), a window of
another process ordered behind every other window:

- `CaptureWindow` returns 800x664 (400x332 points at 2x) with the magenta
  swatch and white background within ±6 per channel after the round trip to
  sRGB.
- **Fully covered**: testwin puts an opaque window of its own exactly over
  itself (AppKit then reports it occluded), the test types into it with
  `input.Target(pid).TypeString`, the page turns its swatch cyan, and
  `CaptureWindow` shows the cyan. So the capture is of live content from a
  window nobody can see, not a stale buffer.
- The frontmost app is the same before and after every test.

`Capture` (the whole-display path) is written, compiled and lint clean but
**not run on hardware**: it reads whatever is on the user's screen, and its
test brings testwin to the front, so it lives under the `hwtest_global` tag
with the global input tests and runs only in a VM, on explicit request.

## Implementation notes

ScreenCaptureKit is asynchronous: every call takes an Objective-C completion
block that runs later on a dispatch queue. That lives alone in
[`sck_darwin.go`](sck_darwin.go), where each call becomes a channel receive
with a 10 s deadline. A block is released only after it has run; on a timeout
it is deliberately leaked, because ScreenCaptureKit may still call it.

`-[SCWindow frame]` returns a 32-byte `CGRect`, which on amd64 goes through
`objc_msgSend_stret`; purego picks that. `TestRectByValue` and `TestRGBA`
check the `CGRect` ABI at run time (arm64 on hardware, amd64 on the CI Intel
runner) without capturing anything.

## Tests

```bash
go test ./screen                 # unit tests, capture nothing
go test -tags hwtest ./screen    # capture testwin in the background; needs Screen Recording
```

## Example

[`examples/screen`](../examples/screen) reports the display size and
permission, or saves a window as PNG:

```bash
go run ./examples/screen
go run ./examples/screen -window 1234 -o window.png
```
