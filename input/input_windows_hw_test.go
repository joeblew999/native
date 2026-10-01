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
	"testing"
	"time"

	"github.com/crgimenes/native/input"
	"github.com/crgimenes/native/internal/testwin"
)

const landWithin = 3 * time.Second

func start(t *testing.T) (*testwin.Win, *input.App, int, int) {
	t.Helper()
	front := testwin.Frontmost(t)
	x, y, err := input.MousePosition()
	if err != nil {
		t.Fatal(err)
	}
	w := testwin.Start(t, 120, 140)
	t.Cleanup(func() {
		if after := testwin.Frontmost(t); after != front {
			t.Errorf("foreground window changed: %s -> %s", front, after)
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
	return w, input.Target(w.PID), int(s[0].Num("border")), int(s[0].Num("titlebar"))
}

func TestAppKeyTap(t *testing.T) {
	w, app, _, _ := start(t)
	err := app.KeyTap(input.KeyA)
	if err != nil {
		t.Fatal(err)
	}
	e := w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "key" })
	if e.Str("key") != "a" || e.Str("code") != "KeyA" {
		t.Errorf("got key %q code %q, want a / KeyA", e.Str("key"), e.Str("code"))
	}
	err = app.KeyTap(input.KeyReturn)
	if err != nil {
		t.Fatal(err)
	}
	e = w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "key" && e.Str("key") != "a" })
	if e.Str("key") != "Enter" {
		t.Errorf("got key %q, want Enter", e.Str("key"))
	}
}

// TestAppKeyTapModifier records what a modifier does: a posted message cannot
// set the target's keyboard state, which is where Chromium reads modifiers.
func TestAppKeyTapModifier(t *testing.T) {
	w, app, _, _ := start(t)
	err := app.KeyTap(input.KeyB, input.ModShift)
	if err != nil {
		t.Fatal(err)
	}
	e := w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "key" && e.Str("code") == "KeyB" })
	mods, _ := e["mods"].([]any)
	t.Logf("Shift+B arrived as key %q mods %v", e.Str("key"), mods)
}

func TestAppTypeString(t *testing.T) {
	w, app, _, _ := start(t)
	const s = "héllo, wörld 👋 日本"
	err := app.TypeString(s)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 5*time.Second, func(e testwin.Event) bool {
		return e.Type() == "input" && e.Str("value") == s
	})
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

func TestAppNoWindow(t *testing.T) {
	// PID 4 is the System process, which owns no window.
	err := input.Target(4).Click(1, 1, input.Left)
	if !errors.Is(err, input.ErrNoWindow) {
		t.Fatalf("err %v, want ErrNoWindow", err)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
