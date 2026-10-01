//go:build hwtest && windows

// Hardware tests for the per-application half on Windows: they launch
// examples/testwin (a glaze/WebView2 window) as a separate process, behind
// everything and never activated, and drive it only through Target(pid). The
// foreground window and the cursor must be the same afterwards; every test
// checks both.
//
//	go test -tags hwtest ./input
package input_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/crgimenes/native/input"
	"github.com/crgimenes/native/internal/testwin"
)

const landWithin = 3 * time.Second

func start(t *testing.T, flags ...string) (*testwin.Win, *input.App, int, int) {
	t.Helper()
	front := testwin.Frontmost(t)
	x, y, err := input.MousePosition()
	if err != nil {
		t.Fatal(err)
	}
	w := testwin.Start(t, 120, 140, flags...)
	t.Cleanup(func() {
		// testwin (or anything it started) taking the foreground is the
		// failure; on a CI runner other processes can move it on their own.
		if after := testwin.Frontmost(t); after != front {
			if testwin.ForegroundPID() == w.PID {
				t.Errorf("testwin took the foreground: %s -> %s", front, after)
			} else {
				t.Logf("foreground changed, not to testwin: %s -> %s", front, after)
			}
		}
		ax, ay, err := input.MousePosition()
		if err != nil {
			t.Error(err)
		}
		if ax != x || ay != y {
			t.Errorf("cursor moved during the test: (%d,%d) -> (%d,%d)", x, y, ax, ay)
		}
	})
	// WebView2 may still be wiring its windows up when the page says ready.
	time.Sleep(500 * time.Millisecond)
	w.Drain()
	t.Logf("testwin pid %d hwnd %#x\n%s", w.PID, w.Window, input.Targets(w.PID))
	s := w.Seen()
	app := input.Target(w.PID)
	bx, tb := int(s[0].Num("border")), int(s[0].Num("titlebar"))
	if !hasFlag(flags, "-blur") {
		focusPage(t, w, app, bx, tb)
	}
	return w, app, bx, tb
}

func TestAppKeyTap(t *testing.T) {
	w, app, _, _ := start(t)
	e := keyed(t, w, app, func() error { return app.KeyTap(input.KeyA) },
		func(e testwin.Event) bool { return e.Type() == "key" && e.Str("code") == "KeyA" })
	if e.Str("key") != "a" {
		t.Errorf("got key %q, want a", e.Str("key"))
	}
	keyed(t, w, app, func() error { return app.KeyTap(input.KeyReturn) },
		func(e testwin.Event) bool { return e.Type() == "key" && e.Str("key") == "Enter" })
}

// TestAppKeyTapModifier records what a modifier does: a posted message cannot
// set the target's keyboard state, which is where Chromium reads modifiers.
func TestAppKeyTapModifier(t *testing.T) {
	w, app, _, _ := start(t)
	e := keyed(t, w, app, func() error { return app.KeyTap(input.KeyB, input.ModShift) },
		func(e testwin.Event) bool { return e.Type() == "key" && e.Str("code") == "KeyB" })
	mods, _ := e["mods"].([]any)
	t.Logf("Shift+B arrived as key %q mods %v", e.Str("key"), mods)
}

func TestAppTypeString(t *testing.T) {
	w, app, _, _ := start(t)
	const s = "héllo, wörld 👋 日本"
	keyed(t, w, app, func() error { return app.TypeString(s) },
		func(e testwin.Event) bool { return e.Type() == "input" && strings.HasSuffix(e.Str("value"), s) })
}

func TestAppClick(t *testing.T) {
	w, app, bx, tb := start(t)
	// The button spans x 10-170, y 160-220 in the page.
	err := app.Click(bx+90, tb+190, input.Left)
	if err != nil {
		t.Fatal(err)
	}
	e := w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "click" })
	if e.Num("count") != 1 || e.Num("button") != 0 {
		t.Errorf("click landed as %v, want the button's first left click", e)
	}
	if abs(e.Num("x")-90) > 2 || abs(e.Num("y")-190) > 2 {
		t.Errorf("click landed at page (%v,%v), want (90,190)", e.Num("x"), e.Num("y"))
	}

	err = app.Click(bx+300, tb+60, input.Right)
	if err != nil {
		t.Fatal(err)
	}
	e = w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "click" && e.Num("button") != 0 })
	if e.Num("button") != 2 {
		t.Errorf("right click landed as %v, want button 2", e)
	}
}

func TestAppScroll(t *testing.T) {
	w, app, _, _ := start(t)
	err := app.Scroll(0, -3)
	if err != nil {
		t.Fatal(err)
	}
	e := w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "wheel" })
	if e.Num("dy") <= 0 {
		t.Errorf("scroll down arrived as deltaY %v, want > 0", e.Num("dy"))
	}
	w.Drain()
	err = app.Scroll(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	e = w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "wheel" && e.Num("dx") != 0 })
	if e.Num("dx") >= 0 {
		t.Errorf("scroll left arrived as deltaX %v, want < 0", e.Num("dx"))
	}
}

// TestAppScrollCovered records what happens to a wheel event aimed at a
// WebView2 window that another window covers completely: Chromium routes
// wheel messages by WindowFromPoint and drops one whose point is over another
// process's window, so nothing is expected to arrive.
func TestAppScrollCovered(t *testing.T) {
	w, app, _, _ := start(t, "-cover")
	w.WaitSeen(t, 10*time.Second, func(e testwin.Event) bool { return e.Type() == "covered" })
	w.Drain()
	err := app.Scroll(0, -3)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := w.Next(1500*time.Millisecond, func(e testwin.Event) bool { return e.Type() == "wheel" }); ok {
		t.Logf("wheel landed in the covered window: %v", e)
		return
	}
	t.Log("wheel did not land in the covered window (expected for Chromium)")
}

// TestAppTypeStringCovered records whether text reaches a WebView2 window
// that another window covers completely, with WebView2's native occlusion
// tracking on (the default) and off.
func TestAppTypeStringCovered(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"default", []string{"-cover"}},
		{"no-occlusion", []string{"-cover", "-no-occlusion"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, app, _, _ := start(t, tc.flags...)
			w.WaitSeen(t, 10*time.Second, func(e testwin.Event) bool { return e.Type() == "covered" })
			time.Sleep(time.Second)
			w.Drain()
			err := app.TypeString("x")
			if err != nil {
				t.Fatal(err)
			}
			_, ok := w.Next(2*time.Second, func(e testwin.Event) bool { return e.Type() == "input" })
			t.Logf("covered, %s: text landed %v; events %v\n%s", tc.name, ok, w.Seen(), input.Targets(w.PID))
		})
	}
}

// TestAppTypeStringBlurred measures the case that matters most in practice:
// an app the user switched away from has no focus window in its thread, and
// Chromium has blurred its page. Text posted then is expected to be dropped;
// a background click on the field gives the page focus back (inside the
// target only), after which text lands. The foreground must not move.
func TestAppTypeStringBlurred(t *testing.T) {
	w, app, bx, tb := start(t, "-blur")
	w.WaitSeen(t, 5*time.Second, func(e testwin.Event) bool { return e.Type() == "blurred" })
	time.Sleep(500 * time.Millisecond)
	w.Drain()
	t.Logf("after blur:\n%s", input.Targets(w.PID))
	err := app.TypeString("a")
	if err != nil {
		t.Fatal(err)
	}
	_, ok := w.Next(1500*time.Millisecond, func(e testwin.Event) bool { return e.Type() == "input" })
	t.Logf("text into the blurred page landed: %v", ok)

	// The text field spans page x 10-370, y 120-144.
	err = app.Click(bx+100, tb+132, input.Left)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "click" })
	time.Sleep(200 * time.Millisecond)
	t.Logf("after the click:\n%s", input.Targets(w.PID))
	keyed(t, w, app, func() error { return app.TypeString("é👋") },
		func(e testwin.Event) bool { return e.Type() == "input" && strings.HasSuffix(e.Str("value"), "é👋") })
}

func TestAppNoWindow(t *testing.T) {
	// PID 4 is the System process, which owns no window.
	err := input.Target(4).Click(1, 1, input.Left)
	if !errors.Is(err, input.ErrNoWindow) {
		t.Fatalf("err %v, want ErrNoWindow", err)
	}
}

// focusPage makes sure the page has focus before a test that needs it. Keys
// only reach a page Chromium considers focused (see TestAppTypeStringBlurred),
// and a cold WebView2 start in a window that is never activated was measured
// ending without focus, or losing it again a moment later (arm runner). So
// give it focus the way a caller would, with a background click on the text
// field, until it holds.
func focusPage(t *testing.T, w *testwin.Win, app *input.App, bx, tb int) {
	t.Helper()
	for try := 1; !w.PageFocused() && try <= 5; try++ {
		t.Logf("page not focused: background click on the text field (%d)", try)
		err := app.Click(bx+100, tb+132, input.Left)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		w.Drain()
	}
}

// keyed sends keyboard input with do and waits for match, giving the page
// focus first (focusPage) and trying again if focus was lost in between.
// testwin's window has WS_EX_NOACTIVATE so it can never take the foreground,
// and in that state the page was measured losing focus again at times after
// Chromium took it; the attempt count is logged so a run shows how often.
func keyed(t *testing.T, w *testwin.Win, app *input.App, do func() error, match func(testwin.Event) bool) testwin.Event {
	t.Helper()
	s := w.Seen()
	bx, tb := int(s[0].Num("border")), int(s[0].Num("titlebar"))
	for try := 1; try <= 5; try++ {
		focusPage(t, w, app, bx, tb)
		err := do()
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := w.Next(1500*time.Millisecond, match); ok {
			t.Logf("landed on attempt %d", try)
			return e
		}
		t.Logf("attempt %d did not land; page focused: %v", try, w.PageFocused())
	}
	t.Fatalf("keyboard input never landed; saw %v\n%s", w.Seen(), input.Targets(w.PID))
	return nil
}

func hasFlag(flags []string, f string) bool {
	for _, g := range flags {
		if g == f {
			return true
		}
	}
	return false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
