// Package input synthesizes mouse and keyboard events, cgo-free.
//
// It has two halves, and the difference matters to whoever is sitting at the
// machine:
//
//   - Per-application: Target(pid) returns an *App whose methods deliver
//     events straight to that process. The real cursor does not move, the
//     frontmost application does not change, and the user's keyboard focus
//     stays where it was. Which kinds of event an application actually acts on
//     while it is in the background is up to the application and the OS
//     version; see the package README for what has been measured.
//
//   - Global: MoveMouse, Click, KeyTap and the rest post into the system event
//     stream exactly as if the hardware had produced them. They move the real
//     cursor and type into whatever has focus.
//
//     app := input.Target(pid)
//     app.KeyTap(input.KeyA, input.ModCmd)
//     app.TypeString("héllo 👋")
//     app.Click(40, 60, input.Left)
//
// Both halves need the Accessibility permission (Trusted reports it); without
// it they return ErrNotTrusted rather than posting events the OS would drop in
// silence.
//
// Implemented on macOS through Quartz Event Services (CGEventPostToPid and
// CGEventPost). Elsewhere every call returns ErrUnsupported.
package input

import "errors"

var (
	// ErrUnsupported is returned on a platform with no input backend.
	ErrUnsupported = errors.New("input: not supported on this platform")
	// ErrNotTrusted is returned when the process lacks the Accessibility
	// permission that posting events needs. Grant it in System Settings,
	// Privacy & Security, Accessibility; this package never prompts for it.
	ErrNotTrusted = errors.New("input: accessibility permission not granted")
	// ErrNoWindow is returned by a window-relative App method when the target
	// process has no on-screen window.
	ErrNoWindow = errors.New("input: target has no on-screen window")
	// ErrUnknownKey is returned for a Key or Button value this package does
	// not define.
	ErrUnknownKey = errors.New("input: unknown key or button")
)

// Button is a mouse button.
type Button int

const (
	Left Button = iota
	Right
	Middle
)

// Modifier is a modifier key held down for the duration of a KeyTap.
type Modifier int

const (
	ModShift Modifier = iota + 1
	ModCtrl
	ModAlt // Option on macOS
	ModCmd // Command on macOS, the Windows key elsewhere
)

// Key is a physical key, named for its US-layout legend. Letters and digits
// are positional: KeyA is the key labelled A on a US keyboard, whatever the
// active layout makes it type. To produce a particular character regardless of
// layout, use TypeString.
type Key int

const (
	KeyA Key = iota + 1
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
	Key0
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	KeyReturn
	KeyTab
	KeySpace
	KeyBackspace
	KeyDelete // forward delete
	KeyEscape
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

// Trusted reports whether this process holds the Accessibility permission
// that posting events needs. It never prompts. On a platform with no backend
// it returns false.
func Trusted() bool { return trusted() }

// App is one target application. Its methods deliver events to that process
// only, without moving the cursor or changing focus.
type App struct {
	pid int
}

// Target returns the application with the given process ID. It does not check
// that the process exists; the methods report that.
func Target(pid int) *App { return &App{pid: pid} }

// Click presses and releases button b at (x, y), in points from the top-left
// corner of the target's frontmost on-screen window (its frame, title bar
// included).
func (a *App) Click(x, y int, b Button) error { return a.click(x, y, b) }

// Scroll scrolls the target's frontmost window by dx, dy lines, with the
// pointer placed at the window's centre. Positive dy scrolls up and positive
// dx scrolls left, the direction of the Quartz and robotgo conventions.
func (a *App) Scroll(dx, dy int) error { return a.scroll(dx, dy) }

// KeyTap presses and releases key k with the given modifiers held.
func (a *App) KeyTap(k Key, mods ...Modifier) error { return a.keyTap(k, mods) }

// TypeString types s as text, one character at a time, bypassing the keyboard
// layout: any Unicode, emoji included, arrives as itself.
func (a *App) TypeString(s string) error { return a.typeString(s) }

// MoveMouse moves the real cursor to (x, y), in points from the top-left of
// the main display.
func MoveMouse(x, y int) error { return moveMouse(x, y) }

// MousePosition returns the real cursor position, in points from the top-left
// of the main display.
func MousePosition() (x, y int, err error) { return mousePosition() }

// Click presses and releases button b at the cursor's current position,
// delivering to whatever is under it.
func Click(b Button) error { return click(b) }

// MouseDown presses button b at the cursor's current position.
func MouseDown(b Button) error { return mouseToggle(b, true) }

// MouseUp releases button b at the cursor's current position.
func MouseUp(b Button) error { return mouseToggle(b, false) }

// Scroll scrolls whatever is under the cursor by dx, dy lines; the sign
// convention is App.Scroll's.
func Scroll(dx, dy int) error { return scroll(dx, dy) }

// KeyTap presses and releases key k, with the given modifiers held, into
// whatever has keyboard focus.
func KeyTap(k Key, mods ...Modifier) error { return keyTap(k, mods) }

// TypeString types s into whatever has keyboard focus; see App.TypeString.
func TypeString(s string) error { return typeString(s) }
