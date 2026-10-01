// The Windows testwin: the same page in a glaze (WebView2) window that this
// process creates itself and hands to glaze, so it controls how the window is
// shown.
//
// By default it never takes the foreground. The window has WS_EX_NOACTIVATE
// and is shown with SW_SHOWNOACTIVATE before glaze sees it: without the
// extended style, glaze's start-up (ShowWindow(SW_SHOW), then MoveFocus,
// which makes WebView2 call SetFocus and so activate the top-level window)
// took the foreground on GitHub's windows-latest runner. It is shown on top of
// the Z order, not behind everything as on macOS, because Chromium drops a
// wheel event whose point is over another process's window; -cover hides it
// for the tests that want it hidden. Whatever the user (or the CI session)
// had in front stays in front. -front reverses all that for the
// hwtest_global tests.
//
// The window's class uses DefWindowProcW directly as its window procedure
// (glaze subclasses it for what it needs), so no Go callback is handed to
// Windows. "window" in the start line is the HWND; "titlebar" and "border"
// are the offsets from the top-left of the frame DWM draws (the window as
// the user sees it, without the invisible resize borders) to the top-left of
// the web content, in pixels; "clientx"/"clienty" are that content's
// top-left on screen.

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/crgimenes/glaze"
)

func init() { runtime.LockOSThread() }

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procAdjustWindowRectEx  = user32.NewProc("AdjustWindowRectEx")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procGetWindow           = user32.NewProc("GetWindow")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	procAttachThreadInput   = user32.NewProc("AttachThreadInput")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procCreateSolidBrush    = gdi32.NewProc("CreateSolidBrush")
	procDwmGetWindowAttrib  = dwmapi.NewProc("DwmGetWindowAttribute")
	procSetDisplayAffinity  = user32.NewProc("SetWindowDisplayAffinity")
)

const (
	wsOverlapped = 0x00000000
	wsCaption    = 0x00C00000
	wsSysMenu    = 0x00080000
	wsPopup      = 0x80000000

	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000

	swShowNoActivate = 4
	swShow           = 5

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040

	gwHwndPrev = 3

	idcArrow = 32512

	dwmwaExtFrame = 9
	wdaMonitor    = 1

	winW, winH = 400, 300
)

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

// wndClassExW is WNDCLASSEXW.
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

func call(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func u16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		fail(err)
	}
	return p
}

// register makes a window class whose procedure is DefWindowProcW itself.
func register(name string, brush uintptr) *uint16 {
	cls := u16(name)
	wc := wndClassExW{
		lpfnWndProc:   procDefWindowProcW.Addr(),
		hInstance:     call(procGetModuleHandleW, 0),
		hCursor:       call(procLoadCursorW, 0, idcArrow),
		hbrBackground: brush,
		lpszClassName: cls,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if call(procRegisterClassExW, uintptr(unsafe.Pointer(&wc))) == 0 {
		fail(fmt.Errorf("RegisterClassExW %s failed", name))
	}
	return cls
}

func frameOf(hwnd uintptr) rect {
	var r rect
	if call(procDwmGetWindowAttrib, hwnd, dwmwaExtFrame, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)) != 0 || r.Right <= r.Left {
		call(procGetWindowRect, hwnd, uintptr(unsafe.Pointer(&r)))
	}
	return r
}

func main() {
	x := flag.Int("x", 80, "window left edge, in pixels from the left of the main screen")
	y := flag.Int("y", 80, "window top edge, in pixels from the top of the main screen")
	front := flag.Bool("front", false, "take the foreground instead: only for the hwtest_global tests, which drive the real cursor and keyboard and run in a VM")
	cover := flag.Bool("cover", false, "a second after start, cover the window completely with another opaque window of this process, and report \"covered\"")
	protect := flag.Bool("protect", false, "exclude the window from capture (SetWindowDisplayAffinity WDA_MONITOR): the negative control for screen's blank-frame check")
	noOcclusion := flag.Bool("no-occlusion", false, "start WebView2 with --disable-features=CalculateNativeWinOcclusion, so a fully covered window is not treated as hidden")
	life := flag.Duration("timeout", 5*time.Minute, "exit after this long")
	flag.Parse()
	exitWithParent(*life)
	if *noOcclusion {
		// WebView2 reads extra browser flags from the environment.
		err := os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-features=CalculateNativeWinOcclusion")
		if err != nil {
			fail(err)
		}
	}

	// White, as the page: the class brush is what shows before WebView2 draws.
	cls := register("native-testwin", call(procCreateSolidBrush, 0xffffff))
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu)
	exStyle := uintptr(wsExNoActivate)
	if *front {
		exStyle = 0
	}
	r := rect{0, 0, winW, winH}
	call(procAdjustWindowRectEx, uintptr(unsafe.Pointer(&r)), style, 0, exStyle)
	hwnd := call(procCreateWindowExW, exStyle, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("native testwin"))),
		style, uintptr(*x), uintptr(*y), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
		0, 0, call(procGetModuleHandleW, 0), 0)
	if hwnd == 0 {
		fail(fmt.Errorf("CreateWindowExW failed"))
	}
	if *front {
		takeForeground(hwnd)
	} else {
		call(procShowWindow, hwnd, swShowNoActivate)
	}

	if *protect && call(procSetDisplayAffinity, hwnd, wdaMonitor) == 0 {
		fail(fmt.Errorf("SetWindowDisplayAffinity failed"))
	}

	w, err := glaze.NewWithOptions(glaze.Options{Window: ptr(hwnd)})
	if err != nil {
		fail(err)
	}
	defer w.Destroy()
	err = w.Bind("report", func(v map[string]any) { emit(v) })
	if err != nil {
		fail(err)
	}
	f := frameOf(hwnd)
	var c point
	call(procClientToScreen, hwnd, uintptr(unsafe.Pointer(&c)))
	emit(map[string]any{
		"type":     "start",
		"pid":      os.Getpid(),
		"window":   uint32(hwnd), // #nosec G115 -- HWNDs are 32-bit values on every Windows
		"titlebar": c.Y - f.Top,
		"border":   c.X - f.Left,
		// The web content's top-left on screen, for the global tests.
		"clientx": c.X,
		"clienty": c.Y,
	})
	w.SetHtml(page)
	go func() {
		time.Sleep(time.Second)
		w.Dispatch(func() { emit(state("state", hwnd)) })
		if !*cover {
			return
		}
		w.Dispatch(func() { coverWith(hwnd) })
		time.Sleep(700 * time.Millisecond)
		w.Dispatch(func() { emit(state("covered", hwnd)) })
	}()
	w.Run()
}

// takeForeground brings hwnd to the front for the global tests. Windows
// refuses SetForegroundWindow to a process that is not already in front
// (measured on the windows-11-arm runner); attaching to the foreground
// thread's input state for the call is the documented way past that.
func takeForeground(hwnd uintptr) {
	call(procShowWindow, hwnd, swShow)
	fg := call(procGetForegroundWindow)
	self := call(procGetCurrentThreadId)
	other := call(procGetWindowThreadPID, fg, 0)
	if fg != 0 && other != self {
		call(procAttachThreadInput, self, other, 1)
		defer call(procAttachThreadInput, self, other, 0)
	}
	call(procBringWindowToTop, hwnd)
	call(procSetForegroundWindow, hwnd)
}

func state(typ string, hwnd uintptr) map[string]any {
	fg := call(procGetForegroundWindow)
	return map[string]any{
		"type":    typ,
		"active":  fg == hwnd,
		"key":     fg == hwnd,
		"visible": call(procIsWindowVisible, hwnd) != 0,
		"exposed": !coveredBy(hwnd),
	}
}

// coveredBy reports whether a visible window above hwnd in the Z order
// contains its whole frame.
func coveredBy(hwnd uintptr) bool {
	f := frameOf(hwnd)
	for h := call(procGetWindow, hwnd, gwHwndPrev); h != 0; h = call(procGetWindow, h, gwHwndPrev) {
		if call(procIsWindowVisible, h) == 0 {
			continue
		}
		var r rect
		call(procGetWindowRect, h, uintptr(unsafe.Pointer(&r)))
		if r.Left <= f.Left && r.Top <= f.Top && r.Right >= f.Right && r.Bottom >= f.Bottom {
			return true
		}
	}
	return false
}

// coverWith puts an opaque green window exactly over hwnd's frame, at the
// top of the Z order, without activating anything.
func coverWith(hwnd uintptr) {
	cls := register("native-testwin-cover", call(procCreateSolidBrush, 0x008000))
	f := frameOf(hwnd)
	c := call(procCreateWindowExW, wsExToolWindow|wsExNoActivate, uintptr(unsafe.Pointer(cls)), 0, wsPopup,
		uintptr(f.Left), uintptr(f.Top), uintptr(f.Right-f.Left), uintptr(f.Bottom-f.Top),
		0, 0, call(procGetModuleHandleW, 0), 0)
	if c == 0 {
		fail(fmt.Errorf("CreateWindowExW (cover) failed"))
	}
	// HWND_TOP (0): above testwin wherever testwin is, still without
	// activating anything.
	call(procSetWindowPos, c, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
}

// ptr reinterprets a native handle as the unsafe.Pointer glaze takes; spelled
// this way only to quiet go vet's unsafeptr check.
func ptr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u)) // #nosec G103 -- an HWND, not Go memory
}
