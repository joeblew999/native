//go:build hwtest && windows

package input

import (
	"fmt"
	"syscall"
	"unsafe"
)

const gwHwndPrev = 3

var (
	procGetWindowTextW      = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowTextW")
	procGetForegroundWindow = syscall.NewLazyDLL("user32.dll").NewProc("GetForegroundWindow")
)

var (
	procOpenProcess                = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	procQueryFullProcessImageNameW = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

// exe names the process's executable, or "" when it cannot be opened.
func exe(pid uint32) string {
	const processQueryLimitedInformation = 0x1000
	h := call(procOpenProcess, processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer func() { _ = syscall.CloseHandle(syscall.Handle(h)) }()
	var buf [512]uint16
	n := uint32(len(buf))
	if call(procQueryFullProcessImageNameW, h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))) == 0 {
		return ""
	}
	s := syscall.UTF16ToString(buf[:n])
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' {
			return s[i+1:]
		}
	}
	return s
}

func title(h uintptr) string {
	var buf [256]uint16
	n := call(procGetWindowTextW, h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// cloaked reads DWMWA_CLOAKED (14): a cloaked window is "visible" to user32
// but not drawn.
func cloaked(h uintptr) uint32 {
	var v uint32
	call(procDwmGetWindowAttribute, h, 14, uintptr(unsafe.Pointer(&v)), 4)
	return v
}

// Targets describes the windows the App methods would post to for pid: the
// top-level window, the keyboard target and the mouse target at the window's
// centre, each with its class and owning process. The hardware tests log it,
// so a CI run records which WebView2 window the events went to.
func Targets(pid int) string {
	top, r, err := Target(pid).window()
	if err != nil {
		return err.Error()
	}
	d := func(h uintptr) string {
		p, _ := windowPID(h)
		return fmt.Sprintf("%#x %s (pid %d %s)", h, className(h), p, exe(p))
	}
	ex, ey, shows := exposedPoint(top, r)
	s := fmt.Sprintf("top %s frame %v\nkeys -> %s\nmouse -> %s\nwheel point (%d,%d) exposed=%v\ntree:",
		d(top), r, d(keyWindow(top)), d(childAt(top, (r.Left+r.Right)/2, (r.Top+r.Bottom)/2)), ex, ey, shows)
	var walk func(h uintptr, depth int)
	walk = func(h uintptr, depth int) {
		for _, c := range children(h) {
			wr, _ := windowRect(c)
			s += fmt.Sprintf("\n%*s%s visible=%v rect=%v", depth*2, "", d(c), call(procIsWindowVisible, c) != 0, wr)
			walk(c, depth+1)
		}
	}
	walk(top, 1)
	cx, cy := (r.Left+r.Right)/2, (r.Top+r.Bottom)/2
	under := windowFromPoint(cx, cy)
	s += fmt.Sprintf("\nWindowFromPoint(centre) = %s, root %s %q cloaked=%v", d(under), d(call(procGetAncestor, under, gaRoot)), title(under), cloaked(call(procGetAncestor, under, gaRoot)))
	_, tid := windowPID(top)
	gi := guiThreadInfo{cbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	call(procGetGUIThreadInfo, uintptr(tid), uintptr(unsafe.Pointer(&gi)))
	s += fmt.Sprintf("\ntarget GUI thread: active %s, focus %s; foreground %s", d(gi.hwndActive), d(gi.hwndFocus), d(call(procGetForegroundWindow)))
	s += "\nvisible windows above it:"
	for h := call(procGetWindow, top, gwHwndPrev); h != 0; h = call(procGetWindow, h, gwHwndPrev) {
		if call(procIsWindowVisible, h) == 0 {
			continue
		}
		wr, _ := windowRect(h)
		s += fmt.Sprintf("\n  %s %q rect=%v exstyle=%#x cloaked=%v", d(h), title(h), wr, call(procGetWindowLongW, h, gwlExStyle), cloaked(h))
	}
	return s
}
