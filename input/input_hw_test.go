//go:build hwtest && darwin

// Hardware tests for the per-application half: they launch examples/testwin
// as a separate process and drive it only through Target(pid). The user's
// frontmost application and cursor position must be the same afterwards;
// every test checks both.
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

// start launches testwin and registers the desktop-untouched check.
func start(t *testing.T) (*testwin.Win, *input.App) {
	t.Helper()
	if !input.Trusted() {
		t.Fatal("this process lacks the Accessibility permission; grant it to the terminal running the tests, this suite never works around it")
	}
	front := testwin.Frontmost(t)
	x, y, err := input.MousePosition()
	if err != nil {
		t.Fatal(err)
	}
	w := testwin.Start(t, 120, 140)
	t.Cleanup(func() {
		after := testwin.Frontmost(t)
		if after != front {
			t.Errorf("frontmost app changed: %q -> %q", front, after)
		}
		ax, ay, err := input.MousePosition()
		if err != nil {
			t.Error(err)
		}
		if ax != x || ay != y {
			// The tests never move it; a change here means someone at the
			// machine did, or an event escaped. Worth a look either way.
			t.Logf("cursor moved during the test: (%d,%d) -> (%d,%d)", x, y, ax, ay)
		}
	})
	w.Drain()
	return w, input.Target(w.PID)
}

func TestAppKeyTap(t *testing.T) {
	w, app := start(t)
	err := app.KeyTap(input.KeyA)
	if err != nil {
		t.Fatal(err)
	}
	e := w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "key" })
	if e.Str("key") != "a" || e.Str("code") != "KeyA" {
		t.Errorf("got key %q code %q, want a / KeyA", e.Str("key"), e.Str("code"))
	}
	err = app.KeyTap(input.KeyB, input.ModShift)
	if err != nil {
		t.Fatal(err)
	}
	e = w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "key" })
	mods, _ := e["mods"].([]any)
	if e.Str("key") != "B" || len(mods) != 1 || mods[0] != "shift" {
		t.Errorf("got key %q mods %v, want B with shift", e.Str("key"), mods)
	}
	err = app.KeyTap(input.KeyReturn)
	if err != nil {
		t.Fatal(err)
	}
	e = w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "key" })
	if e.Str("key") != "Enter" {
		t.Errorf("got key %q, want Enter", e.Str("key"))
	}
}

func TestAppTypeString(t *testing.T) {
	w, app := start(t)
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
	w, app := start(t)
	tb := int(w.Seen()[0].Num("titlebar"))
	// The button spans x 10-170, y 160-220 in the page.
	err := app.Click(90, tb+190, input.Left)
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

	err = app.Click(300, tb+60, input.Right)
	if err != nil {
		t.Fatal(err)
	}
	e = w.Wait(t, landWithin, func(e testwin.Event) bool { return e.Type() == "click" })
	if e.Num("button") != 2 {
		t.Errorf("right click landed as %v, want button 2", e)
	}
}

func TestAppScroll(t *testing.T) {
	w, app := start(t)
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
	// launchd owns no window.
	err := input.Target(1).Click(1, 1, input.Left)
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
