//go:build hwtest && windows

package input

import "fmt"

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
		return fmt.Sprintf("%#x %s (pid %d)", h, className(h), p)
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
	return s
}
