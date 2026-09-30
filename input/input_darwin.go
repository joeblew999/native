// macOS backend: Quartz Event Services through purego. Per-application events
// go through CGEventPostToPid, global ones through CGEventPost at the HID tap.
//
// CGPoint crosses the boundary by value (CGEventCreateMouseEvent,
// CGEventSetLocation, CGEventGetLocation). purego lays that out per the
// platform ABI: two doubles in d0/d1 on arm64 (an HFA), in xmm0/xmm1 on amd64
// (SSE class). TestPointByValue round-trips a point through CoreGraphics to
// prove it at run time, because this does not show up in a cross-compile.

package input

import (
	"fmt"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
)

type cgPoint struct{ X, Y float64 }

type cgRect struct {
	Origin struct{ X, Y float64 }
	Size   struct{ W, H float64 }
}

const (
	eventLeftMouseDown  = 1
	eventLeftMouseUp    = 2
	eventRightMouseDown = 3
	eventRightMouseUp   = 4
	eventMouseMoved     = 5
	eventOtherMouseDown = 25
	eventOtherMouseUp   = 26

	fieldMouseClickState = 1
	// fieldWindowNumber is undocumented: it is the field AppKit reads for
	// -[NSEvent windowNumber]. The window server fills it for real input; a
	// posted event carries 0 and AppKit routes it to no window at all.
	// Found by setting each field in turn on events posted to testwin.
	fieldWindowNumber = 51

	scrollUnitLine = 1

	hidEventTap = 0

	sourceStatePrivate   = -1
	sourceStateHIDSystem = 1

	flagShift   = 0x00020000
	flagControl = 0x00040000
	flagAlt     = 0x00080000
	flagCommand = 0x00100000

	windowListOnScreenOnly   = 1 << 0
	windowListExcludeDesktop = 1 << 4
	cfNumberSInt64Type       = 4
)

var (
	cgEventSourceCreate            func(state int32) uintptr
	cgEventCreate                  func(src uintptr) uintptr
	cgEventCreateMouseEvent        func(src uintptr, typ uint32, p cgPoint, button uint32) uintptr
	cgEventCreateKeyboardEvent     func(src uintptr, key uint16, down bool) uintptr
	cgEventCreateScrollWheelEvent2 func(src uintptr, units uint32, count uint32, w1, w2, w3 int32) uintptr
	cgEventKeyboardSetUnicodeStr   func(ev uintptr, n uint, s *uint16)
	cgEventSetFlags                func(ev uintptr, flags uint64)
	cgEventSetIntegerValueField    func(ev uintptr, field uint32, v int64)
	cgEventGetIntegerValueField    func(ev uintptr, field uint32) int64
	cgEventSetLocation             func(ev uintptr, p cgPoint)
	// cgEventSetWindowLocation is exported by CoreGraphics but not in its
	// headers. It sets the window-local point AppKit reports as
	// -[NSEvent locationInWindow]; without it a routed click lands at the
	// window's corner. Nil when this macOS lacks it.
	cgEventSetWindowLocation    func(ev uintptr, p cgPoint)
	cgEventGetLocation          func(ev uintptr) cgPoint
	cgEventPost                 func(tap uint32, ev uintptr)
	cgEventPostToPid            func(pid int32, ev uintptr)
	cgWindowListCopyWindowInfo  func(opts, relativeTo uint32) uintptr
	cgRectMakeWithDictionaryRep func(dict uintptr, r *cgRect) bool
	axIsProcessTrusted          func() bool

	cfRelease            func(uintptr)
	cfArrayGetCount      func(arr uintptr) int
	cfArrayGetValueAtIdx func(arr uintptr, i int) uintptr
	cfDictionaryGetValue func(dict, key uintptr) uintptr
	cfNumberGetValue     func(num uintptr, typ int, out *int64) bool

	keyOwnerPID, keyNumber, keyLayer, keyBounds uintptr
)

var load = sync.OnceValue(func() error {
	as, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("input: %w", err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("input: %w", err)
	}
	purego.RegisterLibFunc(&cgEventSourceCreate, as, "CGEventSourceCreate")
	purego.RegisterLibFunc(&cgEventCreate, as, "CGEventCreate")
	purego.RegisterLibFunc(&cgEventCreateMouseEvent, as, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, as, "CGEventCreateKeyboardEvent")
	// The ...2 variant: CGEventCreateScrollWheelEvent is variadic, and a
	// variadic call puts its extra arguments on the stack on darwin/arm64,
	// which purego does not do.
	purego.RegisterLibFunc(&cgEventCreateScrollWheelEvent2, as, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeStr, as, "CGEventKeyboardSetUnicodeString")
	purego.RegisterLibFunc(&cgEventSetFlags, as, "CGEventSetFlags")
	purego.RegisterLibFunc(&cgEventSetIntegerValueField, as, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&cgEventGetIntegerValueField, as, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&cgEventSetLocation, as, "CGEventSetLocation")
	purego.RegisterLibFunc(&cgEventGetLocation, as, "CGEventGetLocation")
	sym, err := purego.Dlsym(as, "CGEventSetWindowLocation")
	if err == nil {
		purego.RegisterFunc(&cgEventSetWindowLocation, sym)
	}
	purego.RegisterLibFunc(&cgEventPost, as, "CGEventPost")
	purego.RegisterLibFunc(&cgEventPostToPid, as, "CGEventPostToPid")
	purego.RegisterLibFunc(&cgWindowListCopyWindowInfo, as, "CGWindowListCopyWindowInfo")
	purego.RegisterLibFunc(&cgRectMakeWithDictionaryRep, as, "CGRectMakeWithDictionaryRepresentation")
	purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&cfArrayGetCount, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&cfArrayGetValueAtIdx, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	for _, k := range []struct {
		dst  *uintptr
		name string
	}{
		{&keyOwnerPID, "kCGWindowOwnerPID"},
		{&keyNumber, "kCGWindowNumber"},
		{&keyLayer, "kCGWindowLayer"},
		{&keyBounds, "kCGWindowBounds"},
	} {
		// The symbol is a CFStringRef variable; dlsym gives its address.
		addr, err := purego.Dlsym(as, k.name)
		if err != nil {
			return fmt.Errorf("input: %w", err)
		}
		*k.dst = *(*uintptr)(ptr(addr))
	}
	return nil
})

// ptr reinterprets an address that native code returned; see the README's
// note on go vet's unsafeptr check.
func ptr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u)) // #nosec G103 -- native address, not Go memory
}

func trusted() bool {
	if load() != nil {
		return false
	}
	return axIsProcessTrusted()
}

// ready loads the frameworks and refuses to post without the permission the
// OS would otherwise drop the events for, silently.
func ready() error {
	err := load()
	if err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return ErrNotTrusted
	}
	return nil
}

// poster sends one event somewhere: to a process, or to the HID tap.
type poster struct {
	pid    int32 // 0: global
	source int32
}

var global = poster{source: sourceStateHIDSystem}

func (p poster) post(ev uintptr) {
	if p.pid != 0 {
		cgEventPostToPid(p.pid, ev)
		return
	}
	cgEventPost(hidEventTap, ev)
}

// newSource returns a CGEventSource. A private source keeps the user's live
// modifier state out of events aimed at another application: if they happen
// to be holding Shift, the target must not see it.
func (p poster) newSource() uintptr { return cgEventSourceCreate(p.source) }

// winTarget routes a posted mouse event to one window of the target process:
// the window's number, and the point in it, from its frame's top-left.
type winTarget struct {
	number int64
	local  cgPoint
}

func (w *winTarget) apply(ev uintptr) {
	if w == nil {
		return
	}
	cgEventSetIntegerValueField(ev, fieldWindowNumber, w.number)
	cgEventSetWindowLocation(ev, w.local)
}

func (p poster) mouse(at cgPoint, b Button, down bool, win *winTarget) error {
	var typ, btn uint32
	switch b {
	case Left:
		typ, btn = eventLeftMouseUp, 0
		if down {
			typ = eventLeftMouseDown
		}
	case Right:
		typ, btn = eventRightMouseUp, 1
		if down {
			typ = eventRightMouseDown
		}
	case Middle:
		typ, btn = eventOtherMouseUp, 2
		if down {
			typ = eventOtherMouseDown
		}
	default:
		return ErrUnknownKey
	}
	src := p.newSource()
	defer release(src)
	ev := cgEventCreateMouseEvent(src, typ, at, btn)
	if ev == 0 {
		return fmt.Errorf("input: CGEventCreateMouseEvent failed")
	}
	defer cfRelease(ev)
	cgEventSetIntegerValueField(ev, fieldMouseClickState, 1)
	cgEventSetFlags(ev, 0)
	win.apply(ev)
	p.post(ev)
	return nil
}

func (p poster) click(at cgPoint, b Button, win *winTarget) error {
	err := p.mouse(at, b, true, win)
	if err != nil {
		return err
	}
	// A down and up in the same instant read as nothing to some apps.
	time.Sleep(15 * time.Millisecond)
	return p.mouse(at, b, false, win)
}

func (p poster) scroll(at *cgPoint, dx, dy int, win *winTarget) error {
	src := p.newSource()
	defer release(src)
	ev := cgEventCreateScrollWheelEvent2(src, scrollUnitLine, 2, int32(dy), int32(dx), 0) // #nosec G115 -- line counts
	if ev == 0 {
		return fmt.Errorf("input: CGEventCreateScrollWheelEvent2 failed")
	}
	defer cfRelease(ev)
	cgEventSetFlags(ev, 0)
	if at != nil {
		cgEventSetLocation(ev, *at)
	}
	win.apply(ev)
	p.post(ev)
	return nil
}

func (p poster) keyTap(k Key, mods []Modifier) error {
	code, ok := keycodes[k]
	if !ok {
		return ErrUnknownKey
	}
	var flags uint64
	for _, m := range mods {
		switch m {
		case ModShift:
			flags |= flagShift
		case ModCtrl:
			flags |= flagControl
		case ModAlt:
			flags |= flagAlt
		case ModCmd:
			flags |= flagCommand
		default:
			return ErrUnknownKey
		}
	}
	src := p.newSource()
	defer release(src)
	for _, down := range []bool{true, false} {
		ev := cgEventCreateKeyboardEvent(src, code, down)
		if ev == 0 {
			return fmt.Errorf("input: CGEventCreateKeyboardEvent failed")
		}
		cgEventSetFlags(ev, flags)
		p.post(ev)
		cfRelease(ev)
	}
	return nil
}

func (p poster) typeString(s string) error {
	src := p.newSource()
	defer release(src)
	for _, r := range s {
		// One character per event pair, as UTF-16: a surrogate pair stays in
		// one event, so an emoji never arrives as two halves.
		units := utf16.Encode([]rune{r})
		for _, down := range []bool{true, false} {
			ev := cgEventCreateKeyboardEvent(src, 0, down)
			if ev == 0 {
				return fmt.Errorf("input: CGEventCreateKeyboardEvent failed")
			}
			cgEventSetFlags(ev, 0)
			cgEventKeyboardSetUnicodeStr(ev, uint(len(units)), &units[0])
			p.post(ev)
			cfRelease(ev)
		}
		// yagni: fixed pacing, fast enough for tests and forms; a caller
		// typing into an IME-heavy app may want it configurable.
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

func release(cf uintptr) {
	if cf != 0 {
		cfRelease(cf)
	}
}

// window finds the target's frontmost on-screen normal window: its number and
// its frame in global top-left coordinates. CGWindowListCopyWindowInfo lists
// front to back, so the first match is the frontmost. Reading bounds, PID and
// layer needs no permission (only window titles would).
func (a *App) window() (int64, cgRect, error) {
	var r cgRect
	list := cgWindowListCopyWindowInfo(windowListOnScreenOnly|windowListExcludeDesktop, 0)
	if list == 0 {
		return 0, r, fmt.Errorf("input: CGWindowListCopyWindowInfo failed")
	}
	defer cfRelease(list)
	for i := range cfArrayGetCount(list) {
		d := cfArrayGetValueAtIdx(list, i)
		if number(d, keyOwnerPID) != int64(a.pid) || number(d, keyLayer) != 0 {
			continue
		}
		b := cfDictionaryGetValue(d, keyBounds)
		if b == 0 || !cgRectMakeWithDictionaryRep(b, &r) {
			continue
		}
		return number(d, keyNumber), r, nil
	}
	return 0, r, ErrNoWindow
}

func number(dict, key uintptr) int64 {
	n := cfDictionaryGetValue(dict, key)
	if n == 0 {
		return -1
	}
	var v int64
	if !cfNumberGetValue(n, cfNumberSInt64Type, &v) {
		return -1
	}
	return v
}

// ready is the package ready plus a PID check: the poster treats PID 0 as
// "global", so a zero or negative PID must never get that far.
func (a *App) ready() error {
	if a.pid <= 0 {
		return fmt.Errorf("input: invalid pid %d", a.pid)
	}
	return ready()
}

// windowReady is ready for the mouse methods, which cannot reach a
// background window without CGEventSetWindowLocation.
func (a *App) windowReady() error {
	err := a.ready()
	if err != nil {
		return err
	}
	if cgEventSetWindowLocation == nil {
		return fmt.Errorf("%w: CGEventSetWindowLocation is missing on this macOS", ErrUnsupported)
	}
	return nil
}

func (a *App) poster() poster { return poster{pid: int32(a.pid), source: sourceStatePrivate} } // #nosec G115 -- a pid

func (a *App) click(x, y int, b Button) error {
	err := a.windowReady()
	if err != nil {
		return err
	}
	win, r, err := a.window()
	if err != nil {
		return err
	}
	local := cgPoint{float64(x), float64(y)}
	at := cgPoint{r.Origin.X + local.X, r.Origin.Y + local.Y}
	return a.poster().click(at, b, &winTarget{win, local})
}

func (a *App) scroll(dx, dy int) error {
	err := a.windowReady()
	if err != nil {
		return err
	}
	win, r, err := a.window()
	if err != nil {
		return err
	}
	local := cgPoint{r.Size.W / 2, r.Size.H / 2}
	at := cgPoint{r.Origin.X + local.X, r.Origin.Y + local.Y}
	return a.poster().scroll(&at, dx, dy, &winTarget{win, local})
}

func (a *App) keyTap(k Key, mods []Modifier) error {
	err := a.ready()
	if err != nil {
		return err
	}
	return a.poster().keyTap(k, mods)
}

func (a *App) typeString(s string) error {
	err := a.ready()
	if err != nil {
		return err
	}
	return a.poster().typeString(s)
}

func moveMouse(x, y int) error {
	err := ready()
	if err != nil {
		return err
	}
	src := global.newSource()
	defer release(src)
	ev := cgEventCreateMouseEvent(src, eventMouseMoved, cgPoint{float64(x), float64(y)}, 0)
	if ev == 0 {
		return fmt.Errorf("input: CGEventCreateMouseEvent failed")
	}
	defer cfRelease(ev)
	global.post(ev)
	return nil
}

func mousePosition() (int, int, error) {
	err := load()
	if err != nil {
		return 0, 0, err
	}
	p, err := cursor()
	return int(p.X), int(p.Y), err
}

// cursor reads the pointer location from a fresh, never-posted event.
func cursor() (cgPoint, error) {
	ev := cgEventCreate(0)
	if ev == 0 {
		return cgPoint{}, fmt.Errorf("input: CGEventCreate failed")
	}
	defer cfRelease(ev)
	return cgEventGetLocation(ev), nil
}

func click(b Button) error {
	err := ready()
	if err != nil {
		return err
	}
	at, err := cursor()
	if err != nil {
		return err
	}
	return global.click(at, b, nil)
}

func mouseToggle(b Button, down bool) error {
	err := ready()
	if err != nil {
		return err
	}
	at, err := cursor()
	if err != nil {
		return err
	}
	return global.mouse(at, b, down, nil)
}

func scroll(dx, dy int) error {
	err := ready()
	if err != nil {
		return err
	}
	return global.scroll(nil, dx, dy, nil)
}

func keyTap(k Key, mods []Modifier) error {
	err := ready()
	if err != nil {
		return err
	}
	return global.keyTap(k, mods)
}

func typeString(s string) error {
	err := ready()
	if err != nil {
		return err
	}
	return global.typeString(s)
}

// keycodes maps Key to the macOS virtual key code (kVK_* in
// HIToolbox/Events.h), which names a physical key position.
var keycodes = map[Key]uint16{
	KeyA: 0x00, KeyS: 0x01, KeyD: 0x02, KeyF: 0x03, KeyH: 0x04, KeyG: 0x05,
	KeyZ: 0x06, KeyX: 0x07, KeyC: 0x08, KeyV: 0x09, KeyB: 0x0B, KeyQ: 0x0C,
	KeyW: 0x0D, KeyE: 0x0E, KeyR: 0x0F, KeyY: 0x10, KeyT: 0x11, KeyO: 0x1F,
	KeyU: 0x20, KeyI: 0x22, KeyP: 0x23, KeyL: 0x25, KeyJ: 0x26, KeyK: 0x28,
	KeyN: 0x2D, KeyM: 0x2E,
	Key1: 0x12, Key2: 0x13, Key3: 0x14, Key4: 0x15, Key6: 0x16, Key5: 0x17,
	Key9: 0x19, Key7: 0x1A, Key8: 0x1C, Key0: 0x1D,
	KeyReturn: 0x24, KeyTab: 0x30, KeySpace: 0x31, KeyBackspace: 0x33,
	KeyEscape: 0x35, KeyDelete: 0x75, KeyHome: 0x73, KeyEnd: 0x77,
	KeyPageUp: 0x74, KeyPageDown: 0x79,
	KeyLeft: 0x7B, KeyRight: 0x7C, KeyDown: 0x7D, KeyUp: 0x7E,
	KeyF1: 0x7A, KeyF2: 0x78, KeyF3: 0x63, KeyF4: 0x76, KeyF5: 0x60,
	KeyF6: 0x61, KeyF7: 0x62, KeyF8: 0x64, KeyF9: 0x65, KeyF10: 0x6D,
	KeyF11: 0x67, KeyF12: 0x6F,
}
