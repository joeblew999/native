# input

Synthesize mouse and keyboard events, cgo-free. The core of
[robotgo](https://github.com/go-vgo/robotgo)'s mouse and keyboard, without the C
toolchain.

It comes in two halves, and the difference matters to whoever is sitting at the
machine:

- **Per-application** (`Target(pid)`): events go straight to one process. The
  real cursor does not move, the frontmost app does not change, the user's
  keyboard focus stays where it was. This is the half to use for driving an app
  in the background.
- **Global** (`MoveMouse`, `Click`, `KeyTap`, ...): events enter the system
  event stream as if the hardware produced them. They move the real cursor and
  type into whatever has focus.

```go
import "github.com/crgimenes/native/input"

app := input.Target(pid)
app.KeyTap(input.KeyA, input.ModCmd)
app.TypeString("héllo 👋")
app.Click(40, 60, input.Left) // points from the window's top-left
app.Scroll(0, -3)
```

## API

| Func | Description |
| --- | --- |
| `Trusted() bool` | Whether the process holds the Accessibility permission. Never prompts. |
| `Target(pid int) *App` | One application, addressed by process ID. |
| `(*App).Click(x, y int, b Button) error` | Press and release `b` at `(x, y)` points from the top-left of the app's frontmost window (frame, title bar included). |
| `(*App).Scroll(dx, dy int) error` | Scroll that window by lines, pointer at its centre. `dy > 0` up, `dx > 0` left. |
| `(*App).KeyTap(k Key, mods ...Modifier) error` | Press and release a key with modifiers held. |
| `(*App).TypeString(s string) error` | Type text, bypassing the keyboard layout; any Unicode, emoji included. |
| `MoveMouse(x, y int) error` | Move the real cursor (global points, top-left of the main display). |
| `MousePosition() (x, y int, err error)` | Where the real cursor is. |
| `Click(b Button) error` | Click at the cursor. |
| `MouseDown(b Button) error` / `MouseUp(b Button) error` | Press / release at the cursor. |
| `Scroll(dx, dy int) error` | Scroll whatever is under the cursor. |
| `KeyTap(k Key, mods ...Modifier) error` | Key into whatever has focus. |
| `TypeString(s string) error` | Text into whatever has focus. |

Types: `Button` (`Left`, `Right`, `Middle`), `Modifier` (`ModShift`, `ModCtrl`,
`ModAlt`, `ModCmd`), `Key` (`KeyA`-`KeyZ`, `Key0`-`Key9`, `KeyReturn`, `KeyTab`,
`KeySpace`, `KeyBackspace`, `KeyDelete`, `KeyEscape`, arrows, `KeyHome`,
`KeyEnd`, `KeyPageUp`, `KeyPageDown`, `KeyF1`-`KeyF12`). Keys are physical
positions on a US layout; use `TypeString` for a particular character.

Errors: `ErrUnsupported` (no backend), `ErrNotTrusted` (no Accessibility
permission), `ErrNoWindow` (the target has no on-screen window),
`ErrUnknownKey` (a `Key`, `Modifier` or `Button` value this package does not
define). No native types cross the API.

## Platforms

| OS | Backend | Status |
| --- | --- | --- |
| macOS | Quartz Event Services: `CGEventPostToPid` (per app), `CGEventPost` at the HID tap (global) | per-app: ✅ tested on hardware (arm64); global: code only, see below |
| Windows | — | ⬜ `ErrUnsupported` (separate PR) |
| Linux | — | ⬜ `ErrUnsupported` (separate PR) |

## Permission

Both halves need **Accessibility** (System Settings → Privacy & Security →
Accessibility) for the process, or for the terminal that launched it. Without
it macOS drops posted events without saying so; this package checks first and
returns `ErrNotTrusted` instead. It never prompts and never works around the
permission.

## Background delivery: what lands

Measured 2026-09-30 on macOS 27.0.1 (26A434), Apple silicon, with
`go test -tags hwtest ./input`. The target is
[`examples/testwin`](../examples/testwin), a glaze (WKWebView) window in a
separate process that sits behind every other window, is never key and can
never become active. Every test also asserts the user's frontmost app is the
same afterwards. (testwin is a module of its own, because it needs glaze and
this module depends on purego alone; the tests build it on the fly.)

| Event | Lands in the background? | Notes |
| --- | --- | --- |
| `KeyTap` | ✅ yes | letters, Shift, Return, as `keydown` in the page |
| `TypeString` | ✅ yes | `héllo, wörld 👋 日本` arrives intact in the text field; also lands while the window is fully covered (the screen tests) |
| `Click`, left | ✅ yes, **with the two undocumented calls below** | `mousedown`/`mouseup`/`click` at the exact page point |
| `Click`, right | ✅ yes, same | `contextmenu`, button 2 |
| `Scroll`, vertical and horizontal | ✅ yes, same | `wheel` with the right signs |

**Clicks and scroll depend on undocumented CoreGraphics behaviour.** Posted
with only public API, a mouse or scroll event does reach the target's
`NSApplication`, but `-[NSEvent windowNumber]` is 0, so AppKit hands it to no
window and it vanishes: that is the "background clicks are unreliable" folk
wisdom, measured. Two things fix it:

1. CGEvent **field 51** set to the window's number. It is not in the public
   `CGEventField` enum; it is what AppKit reads as `windowNumber`. Found by
   setting every field in turn on clicks posted to testwin (`-nsevents` shows
   each event as AppKit receives it). The public fields 91 and 92
   (`kCGMouseEventWindowUnderMousePointer...`) do nothing here.
2. **`CGEventSetWindowLocation`**, exported by CoreGraphics but absent from its
   headers, for the window-local point (`-[NSEvent locationInWindow]`). With
   field 51 alone AppKit places the click at the window's top-left corner, in
   the title bar, and the page sees nothing.

If a future macOS drops `CGEventSetWindowLocation`, `App.Click` and
`App.Scroll` return `ErrUnsupported` rather than posting clicks that go
nowhere. Keys use only public API.

What has not been measured, on purpose: a background click on a regular app
(one allowed to activate). AppKit may bring such an app forward on a
`mouseDown`, which would take over the desktop of whoever is using the test
machine, so the harness never tries. Expect it; check it in a VM.

The cursor is never moved by the per-app half: `CGEventPostToPid` hands events
to the process and bypasses the HID system that owns the pointer. (The tests
log cursor movement as well; on a machine in use that is the user's own hand.)

## Global functions

Written, compiled for every target, lint clean, **never run on hardware**:
their tests (`input_global_test.go`, tag `hwtest_global`) move the real cursor
and bring testwin to the front, so they run only in a VM, on explicit request:

```bash
go test -tags hwtest_global ./input   # VM only
```

## ABI

`CGPoint` is passed and returned by value (`CGEventCreateMouseEvent`,
`CGEventSetLocation`, `CGEventGetLocation`): two doubles in `d0`/`d1` on arm64,
`xmm0`/`xmm1` on amd64. purego lays it out; `TestPointByValue` round-trips a
point through CoreGraphics to prove it at run time (arm64 on hardware, amd64 on
the CI Intel runner). The scroll constructor is `CGEventCreateScrollWheelEvent2`
because the original is variadic, and variadic arguments go on the stack on
darwin/arm64, which purego does not do.

## Tests

```bash
go test ./input                  # unit tests, post nothing
go test -tags hwtest ./input     # drive testwin in the background; needs Accessibility
```

## Example

[`examples/input`](../examples/input) reports the permission, or types into a
given PID in the background:

```bash
go run ./examples/input
go run ./examples/input -pid 1234 -type "hello"
```
