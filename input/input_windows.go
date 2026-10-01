// Windows backend. Per-application events are window messages posted to the
// target's own windows (PostMessageW), so they never pass through the system
// input queue: the real cursor stays put and the foreground window does not
// change. Global events go through SendInput, exactly as hardware input would.
//
// Every call is a plain stdcall through syscall's lazy DLLs, the x/sys
// pattern; no struct crosses the boundary by value (POINT-by-value calls such
// as ChildWindowFromPointEx are avoided on purpose, see the README's ABI
// section), and the structs passed by pointer are laid out with Go's own
// alignment rules, which match the C ones on every Windows GOARCH.

package input

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")

	procPostMessageW             = user32.NewProc("PostMessageW")
	procFindWindowExW            = user32.NewProc("FindWindowExW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetWindow                = user32.NewProc("GetWindow")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procScreenToClient           = user32.NewProc("ScreenToClient")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetAncestor              = user32.NewProc("GetAncestor")
	procWindowFromPoint          = user32.NewProc("WindowFromPoint")
	procMapVirtualKeyW           = user32.NewProc("MapVirtualKeyW")
	procSendInput                = user32.NewProc("SendInput")
	procSetCursorPos             = user32.NewProc("SetCursorPos")
	procGetCursorPos             = user32.NewProc("GetCursorPos")

	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmChar        = 0x0102
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105

	mkLButton = 0x0001
	mkRButton = 0x0002
	mkMButton = 0x0010

	wheelDelta = 120

	gwOwner       = 4
	gaParent      = 1
	gaRoot        = 2
	mapvkVKToVSC  = 0
	dwmwaExtFrame = 9 // DWMWA_EXTENDED_FRAME_BOUNDS

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfHWheel     = 0x1000

	keyeventfExtended = 0x0001
	keyeventfKeyUp    = 0x0002
	keyeventfUnicode  = 0x0004

	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // Alt
	vkLWin    = 0x5B
)

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) contains(x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

type point struct{ X, Y int32 }

// guiThreadInfo is GUITHREADINFO.
type guiThreadInfo struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       rect
}

// mouseInput and keybdInput are INPUT with its union filled in as MOUSEINPUT
// and KEYBDINPUT. Go aligns the inner struct on its uintptr, which puts it at
// offset 8 on 64-bit and 4 on 386, as C does; the trailing pad brings the
// keyboard variant up to the union's size (40 / 28 bytes).
type mouseInput struct {
	typ uint32
	mi  struct {
		dx, dy    int32
		mouseData uint32
		flags     uint32
		time      uint32
		extra     uintptr
	}
}

type keybdInput struct {
	typ uint32
	ki  struct {
		vk, scan uint16
		flags    uint32
		time     uint32
		extra    uintptr
	}
	_ [8]byte
}

func trusted() bool { return true }

func call(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

// post sends one message to a window of another process. PostMessageW fails
// with ERROR_ACCESS_DENIED when the target runs at a higher integrity level
// (an elevated app, under UIPI); that is reported, not worked around.
func post(hwnd uintptr, msg uint32, wp, lp uintptr) error {
	r, _, err := procPostMessageW.Call(hwnd, uintptr(msg), wp, lp)
	if r == 0 {
		if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("%w: the target runs at a higher integrity level (UIPI)", ErrNotTrusted)
		}
		return fmt.Errorf("input: PostMessageW: %w", err)
	}
	return nil
}

func makeLParam(x, y int32) uintptr {
	return uintptr(uint32(uint16(int16(x))) | uint32(uint16(int16(y)))<<16) // #nosec G115 -- 16-bit coordinates by definition
}

func windowPID(hwnd uintptr) (pid, tid uint32) {
	tid = uint32(call(procGetWindowThreadProcessId, hwnd, uintptr(unsafe.Pointer(&pid)))) // #nosec G115 -- a thread id
	return pid, tid
}

func className(hwnd uintptr) string {
	var buf [256]uint16
	n := call(procGetClassNameW, hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func windowRect(hwnd uintptr) (rect, bool) {
	var r rect
	return r, call(procGetWindowRect, hwnd, uintptr(unsafe.Pointer(&r))) != 0
}

// frame is the window as the user sees it: DWM's extended frame bounds, which
// leave out the invisible resize borders GetWindowRect includes on Windows
// 10 and later. Falls back to GetWindowRect where DWM does not answer.
func frame(hwnd uintptr) (rect, bool) {
	var r rect
	hr := call(procDwmGetWindowAttribute, hwnd, dwmwaExtFrame, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))
	if hr == 0 && r.Right > r.Left {
		return r, true
	}
	return windowRect(hwnd)
}

// children lists hwnd's direct children, top of the Z order first.
func children(hwnd uintptr) []uintptr {
	var out []uintptr
	var after uintptr
	for {
		after = call(procFindWindowExW, hwnd, after, 0, 0)
		if after == 0 {
			return out
		}
		out = append(out, after)
	}
}

// window finds the target's frontmost visible top-level window: the first in
// the Z order (FindWindowExW with no parent walks it top-down) that belongs
// to the process, is visible, not minimised, and not owned by another window.
func (a *App) window() (uintptr, rect, error) {
	for _, h := range children(0) {
		pid, _ := windowPID(h)
		if int(pid) != a.pid || call(procIsWindowVisible, h) == 0 || call(procIsIconic, h) != 0 {
			continue
		}
		if call(procGetWindow, h, gwOwner) != 0 {
			continue
		}
		r, ok := frame(h)
		if !ok || r.Right <= r.Left || r.Bottom <= r.Top {
			continue
		}
		return h, r, nil
	}
	return 0, rect{}, ErrNoWindow
}

// childAt returns the deepest visible descendant of top containing the screen
// point (x, y), or top itself. Children are walked through FindWindowExW and
// hit-tested on their screen rectangles rather than with ChildWindowFromPoint,
// which takes a POINT by value. For a WebView2 window this descends into the
// browser process's windows, down to Chrome_RenderWidgetHostHWND.
func childAt(top uintptr, x, y int32) uintptr {
	h := top
	for {
		next := uintptr(0)
		for _, c := range children(h) {
			if call(procIsWindowVisible, c) == 0 {
				continue
			}
			r, ok := windowRect(c)
			if ok && r.contains(x, y) {
				next = c
				break
			}
		}
		if next == 0 {
			return h
		}
		h = next
	}
}

// findClass returns the first descendant of hwnd with the given class, breadth
// first, or 0.
func findClass(hwnd uintptr, class string) uintptr {
	queue := children(hwnd)
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if className(h) == class {
			return h
		}
		queue = append(queue, children(h)...)
	}
	return 0
}

// renderWidgetClass is the window Chromium (and so WebView2) gives keyboard
// focus inside the page.
const renderWidgetClass = "Chrome_RenderWidgetHostHWND"

// keyWindow picks the window keyboard messages go to: the window the target's
// GUI thread has focused, if it is a descendant of the target window (for a
// WebView2 host that is Chromium's Chrome_WidgetWin_1 in the browser process,
// whose thread input is attached to the host's); else the parent of the
// Chromium render widget, which is the same window before focus has settled;
// else the top-level window itself. Reading the focus changes nothing.
//
// Measured: keys posted to Chrome_RenderWidgetHostHWND itself are dropped;
// Chrome_WidgetWin_1 takes them.
func keyWindow(top uintptr) uintptr {
	_, tid := windowPID(top)
	gi := guiThreadInfo{cbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if call(procGetGUIThreadInfo, uintptr(tid), uintptr(unsafe.Pointer(&gi))) != 0 &&
		gi.hwndFocus != 0 && gi.hwndFocus != top && call(procGetAncestor, gi.hwndFocus, gaRoot) == top {
		return gi.hwndFocus
	}
	if h := findClass(top, renderWidgetClass); h != 0 {
		return call(procGetAncestor, h, gaParent)
	}
	return top
}

// windowFromPoint wraps WindowFromPoint, which takes a POINT by value. POINT
// is 8 bytes, which both Win64 conventions pass in one integer register (rcx
// on amd64, x0 on arm64) with x in the low half, so packing it into a uintptr
// is the same bits on both; on 386 it is two stack slots, x first.
func windowFromPoint(x, y int32) uintptr {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return call(procWindowFromPoint, uintptr(uint32(x)), uintptr(uint32(y))) // #nosec G115 -- bits of a signed coordinate
	}
	return call(procWindowFromPoint, uintptr(uint64(uint32(x))|uint64(uint32(y))<<32)) // #nosec G115 -- as above
}

// exposedPoint returns a point of the target window's content that is not
// under another window: the centre if that shows, else the first exposed
// point of a grid over the frame. ok is false when the window is entirely
// covered. Chromium routes a wheel message by the window under its point and
// drops it when that window belongs to someone else, so the point matters.
func exposedPoint(top uintptr, r rect) (x, y int32, ok bool) {
	cx, cy := (r.Left+r.Right)/2, (r.Top+r.Bottom)/2
	shows := func(x, y int32) bool {
		h := windowFromPoint(x, y)
		return h != 0 && h != top && call(procGetAncestor, h, gaRoot) == top
	}
	if shows(cx, cy) {
		return cx, cy, true
	}
	const n = 8
	for i := 1; i < n; i++ {
		for j := 1; j < n; j++ {
			x := r.Left + (r.Right-r.Left)*int32(j)/n // #nosec G115 -- small grid
			y := r.Top + (r.Bottom-r.Top)*int32(i)/n  // #nosec G115 -- small grid
			if shows(x, y) {
				return x, y, true
			}
		}
	}
	return cx, cy, false
}

func toClient(hwnd uintptr, x, y int32) point {
	p := point{x, y}
	call(procScreenToClient, hwnd, uintptr(unsafe.Pointer(&p)))
	return p
}

func (a *App) ready() error {
	if a.pid <= 0 {
		return fmt.Errorf("input: invalid pid %d", a.pid)
	}
	return nil
}

func buttonMessages(b Button) (down, up uint32, mk uintptr, err error) {
	switch b {
	case Left:
		return wmLButtonDown, wmLButtonUp, mkLButton, nil
	case Right:
		return wmRButtonDown, wmRButtonUp, mkRButton, nil
	case Middle:
		return wmMButtonDown, wmMButtonUp, mkMButton, nil
	}
	return 0, 0, 0, ErrUnknownKey
}

func (a *App) click(x, y int, b Button) error {
	err := a.ready()
	if err != nil {
		return err
	}
	down, up, mk, err := buttonMessages(b)
	if err != nil {
		return err
	}
	top, r, err := a.window()
	if err != nil {
		return err
	}
	sx, sy := r.Left+int32(x), r.Top+int32(y) // #nosec G115 -- window coordinates
	h := childAt(top, sx, sy)
	p := toClient(h, sx, sy)
	lp := makeLParam(p.X, p.Y)
	// A move first, as hardware would: Chromium hit-tests on it.
	err = post(h, wmMouseMove, 0, lp)
	if err != nil {
		return err
	}
	err = post(h, down, mk, lp)
	if err != nil {
		return err
	}
	// A down and up in the same instant read as nothing to some apps.
	time.Sleep(15 * time.Millisecond)
	return post(h, up, 0, lp)
}

func (a *App) scroll(dx, dy int) error {
	err := a.ready()
	if err != nil {
		return err
	}
	top, r, err := a.window()
	if err != nil {
		return err
	}
	// Fully covered, the centre is posted anyway: an app that does not route
	// by WindowFromPoint still scrolls, Chromium drops it (see the README).
	cx, cy, _ := exposedPoint(top, r)
	h := childAt(top, cx, cy)
	// The wheel messages carry screen coordinates, unlike the button ones.
	lp := makeLParam(cx, cy)
	if dy != 0 {
		err = post(h, wmMouseWheel, wheelParam(dy), lp)
		if err != nil {
			return err
		}
	}
	if dx != 0 {
		// WM_MOUSEHWHEEL counts right as positive; this API counts left.
		err = post(h, wmMouseHWheel, wheelParam(-dx), lp)
	}
	return err
}

// wheelParam packs n lines (WHEEL_DELTA each) into the high word of wParam.
func wheelParam(n int) uintptr {
	return uintptr(uint32(uint16(int16(n*wheelDelta))) << 16) // #nosec G115 -- small line counts
}

func vkScan(vk uint16) uintptr { return call(procMapVirtualKeyW, uintptr(vk), mapvkVKToVSC) }

// keyLParam is the WM_KEYDOWN/UP lParam: repeat count 1, the scan code (which
// Chromium turns into KeyboardEvent.code), the extended bit, the context
// (Alt held) bit, and for key-up the previous-state and transition bits.
func keyLParam(vk uint16, ext, alt, up bool) uintptr {
	lp := uintptr(1) | vkScan(vk)<<16
	if ext {
		lp |= 1 << 24
	}
	if alt {
		lp |= 1 << 29
	}
	if up {
		lp |= 1<<30 | 1<<31
	}
	return lp
}

func modKeys(mods []Modifier) ([]uint16, bool, error) {
	var vks []uint16
	alt := false
	for _, m := range mods {
		switch m {
		case ModShift:
			vks = append(vks, vkShift)
		case ModCtrl:
			vks = append(vks, vkControl)
		case ModAlt:
			vks = append(vks, vkMenu)
			alt = true
		case ModCmd:
			vks = append(vks, vkLWin)
		default:
			return nil, false, ErrUnknownKey
		}
	}
	return vks, alt, nil
}

func (a *App) keyTap(k Key, mods []Modifier) error {
	err := a.ready()
	if err != nil {
		return err
	}
	vk, ok := vkeys[k]
	if !ok {
		return ErrUnknownKey
	}
	mvks, alt, err := modKeys(mods)
	if err != nil {
		return err
	}
	top, _, err := a.window()
	if err != nil {
		return err
	}
	h := keyWindow(top)
	down, up := uint32(wmKeyDown), uint32(wmKeyUp)
	if alt {
		down, up = wmSysKeyDown, wmSysKeyUp
	}
	// Modifier key messages are posted for completeness, but they cannot set
	// the target's keyboard state (GetKeyState), which is where Chromium reads
	// modifiers from; see the README's measured table.
	for _, m := range mvks {
		err = post(h, down, uintptr(m), keyLParam(m, m == vkLWin, alt, false))
		if err != nil {
			return err
		}
	}
	err = post(h, down, uintptr(vk), keyLParam(vk, extended[k], alt, false))
	if err != nil {
		return err
	}
	err = post(h, up, uintptr(vk), keyLParam(vk, extended[k], alt, true))
	if err != nil {
		return err
	}
	for i := len(mvks) - 1; i >= 0; i-- {
		m := mvks[i]
		err = post(h, up, uintptr(m), keyLParam(m, m == vkLWin, alt, true))
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) typeString(s string) error {
	err := a.ready()
	if err != nil {
		return err
	}
	top, _, err := a.window()
	if err != nil {
		return err
	}
	h := keyWindow(top)
	for _, r := range s {
		// One WM_CHAR per UTF-16 unit: a character outside the BMP goes as
		// its high then its low surrogate, which is how Windows itself
		// delivers one, so an emoji arrives whole.
		for _, u := range utf16.Encode([]rune{r}) {
			err = post(h, wmChar, uintptr(u), 1)
			if err != nil {
				return err
			}
		}
		// yagni: fixed pacing, as on macOS.
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// --- global: SendInput ------------------------------------------------------

func sendMouse(ins ...mouseInput) error {
	n, _, err := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return fmt.Errorf("input: SendInput: %w", err)
	}
	return nil
}

func sendKeys(ins []keybdInput) error {
	n, _, err := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return fmt.Errorf("input: SendInput: %w", err)
	}
	return nil
}

func mouseIn(flags, data uint32) mouseInput {
	var in mouseInput
	in.typ = inputMouse
	in.mi.flags = flags
	in.mi.mouseData = data
	return in
}

func keyIn(vk, scan uint16, flags uint32) keybdInput {
	var in keybdInput
	in.typ = inputKeyboard
	in.ki.vk = vk
	in.ki.scan = scan
	in.ki.flags = flags
	return in
}

func buttonFlags(b Button) (down, up uint32, err error) {
	switch b {
	case Left:
		return mouseeventfLeftDown, mouseeventfLeftUp, nil
	case Right:
		return mouseeventfRightDown, mouseeventfRightUp, nil
	case Middle:
		return mouseeventfMiddleDown, mouseeventfMiddleUp, nil
	}
	return 0, 0, ErrUnknownKey
}

func moveMouse(x, y int) error {
	r, _, err := procSetCursorPos.Call(uintptr(x), uintptr(y))
	if r == 0 {
		return fmt.Errorf("input: SetCursorPos: %w", err)
	}
	return nil
}

func mousePosition() (int, int, error) {
	var p point
	r, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if r == 0 {
		return 0, 0, fmt.Errorf("input: GetCursorPos: %w", err)
	}
	return int(p.X), int(p.Y), nil
}

func click(b Button) error {
	down, up, err := buttonFlags(b)
	if err != nil {
		return err
	}
	err = sendMouse(mouseIn(down, 0))
	if err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	return sendMouse(mouseIn(up, 0))
}

func mouseToggle(b Button, isDown bool) error {
	down, up, err := buttonFlags(b)
	if err != nil {
		return err
	}
	if isDown {
		return sendMouse(mouseIn(down, 0))
	}
	return sendMouse(mouseIn(up, 0))
}

func scroll(dx, dy int) error {
	if dy != 0 {
		err := sendMouse(mouseIn(mouseeventfWheel, uint32(int32(dy*wheelDelta)))) // #nosec G115 -- signed delta in a DWORD, as the API defines it
		if err != nil {
			return err
		}
	}
	if dx != 0 {
		return sendMouse(mouseIn(mouseeventfHWheel, uint32(int32(-dx*wheelDelta)))) // #nosec G115 -- as above
	}
	return nil
}

func keyTap(k Key, mods []Modifier) error {
	vk, ok := vkeys[k]
	if !ok {
		return ErrUnknownKey
	}
	mvks, _, err := modKeys(mods)
	if err != nil {
		return err
	}
	var ins []keybdInput
	for _, m := range mvks {
		f := uint32(0)
		if m == vkLWin {
			f = keyeventfExtended
		}
		ins = append(ins, keyIn(m, uint16(vkScan(m)), f)) // #nosec G115 -- a scan code
	}
	f := uint32(0)
	if extended[k] {
		f = keyeventfExtended
	}
	ins = append(ins,
		keyIn(vk, uint16(vkScan(vk)), f),                // #nosec G115 -- a scan code
		keyIn(vk, uint16(vkScan(vk)), f|keyeventfKeyUp)) // #nosec G115 -- a scan code
	for i := len(mvks) - 1; i >= 0; i-- {
		m := mvks[i]
		f := uint32(keyeventfKeyUp)
		if m == vkLWin {
			f |= keyeventfExtended
		}
		ins = append(ins, keyIn(m, uint16(vkScan(m)), f)) // #nosec G115 -- a scan code
	}
	return sendKeys(ins)
}

func typeString(s string) error {
	for _, r := range s {
		// KEYEVENTF_UNICODE with each UTF-16 unit; a surrogate pair goes in
		// one SendInput call so nothing can come between its halves.
		var ins []keybdInput
		for _, u := range utf16.Encode([]rune{r}) {
			ins = append(ins, keyIn(0, u, keyeventfUnicode), keyIn(0, u, keyeventfUnicode|keyeventfKeyUp))
		}
		err := sendKeys(ins)
		if err != nil {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// vkeys maps Key to the Windows virtual-key code. Letters and digits are their
// ASCII codes; MapVirtualKeyW gives the scan code, the physical position.
var vkeys = map[Key]uint16{
	KeyA: 'A', KeyB: 'B', KeyC: 'C', KeyD: 'D', KeyE: 'E', KeyF: 'F', KeyG: 'G',
	KeyH: 'H', KeyI: 'I', KeyJ: 'J', KeyK: 'K', KeyL: 'L', KeyM: 'M', KeyN: 'N',
	KeyO: 'O', KeyP: 'P', KeyQ: 'Q', KeyR: 'R', KeyS: 'S', KeyT: 'T', KeyU: 'U',
	KeyV: 'V', KeyW: 'W', KeyX: 'X', KeyY: 'Y', KeyZ: 'Z',
	Key0: '0', Key1: '1', Key2: '2', Key3: '3', Key4: '4',
	Key5: '5', Key6: '6', Key7: '7', Key8: '8', Key9: '9',
	KeyReturn: 0x0D, KeyTab: 0x09, KeySpace: 0x20, KeyBackspace: 0x08,
	KeyDelete: 0x2E, KeyEscape: 0x1B,
	KeyLeft: 0x25, KeyUp: 0x26, KeyRight: 0x27, KeyDown: 0x28,
	KeyHome: 0x24, KeyEnd: 0x23, KeyPageUp: 0x21, KeyPageDown: 0x22,
	KeyF1: 0x70, KeyF2: 0x71, KeyF3: 0x72, KeyF4: 0x73, KeyF5: 0x74, KeyF6: 0x75,
	KeyF7: 0x76, KeyF8: 0x77, KeyF9: 0x78, KeyF10: 0x79, KeyF11: 0x7A, KeyF12: 0x7B,
}

// extended marks the keys whose scan code carries the E0 prefix: the
// navigation block and the arrows, distinct from their numeric-keypad twins.
var extended = map[Key]bool{
	KeyDelete: true, KeyLeft: true, KeyUp: true, KeyRight: true, KeyDown: true,
	KeyHome: true, KeyEnd: true, KeyPageUp: true, KeyPageDown: true,
}
