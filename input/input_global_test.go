//go:build hwtest_global && darwin

// Global-input tests. They move the real cursor, click, scroll and type into
// whatever has focus, and bring testwin to the front to receive it: run them
// only in a VM or on a machine nobody is using, and only on purpose.
//
//	go test -tags hwtest_global -run Global ./input
package input_test

import (
	"testing"
	"time"

	"github.com/crgimenes/native/input"
	"github.com/crgimenes/native/internal/testwin"
)

func TestGlobal(t *testing.T) {
	if !input.Trusted() {
		t.Fatal("this process lacks the Accessibility permission")
	}
	const wx, wy = 100, 100
	w := testwin.Start(t, wx, wy, "-front")
	tb := int(w.Seen()[0].Num("titlebar"))
	time.Sleep(500 * time.Millisecond) // let activation settle
	w.Drain()

	// The button's middle, page (90,190), in global points.
	bx, by := wx+90, wy+tb+190
	err := input.MoveMouse(bx, by)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	x, y, err := input.MousePosition()
	if err != nil {
		t.Fatal(err)
	}
	if x != bx || y != by {
		t.Errorf("MousePosition = (%d,%d) after MoveMouse(%d,%d)", x, y, bx, by)
	}

	err = input.Click(input.Left)
	if err != nil {
		t.Fatal(err)
	}
	e := w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "click" })
	if e.Num("count") != 1 {
		t.Errorf("click landed as %v, want the button's first click", e)
	}

	err = input.MouseDown(input.Left)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "mousedown" })
	err = input.MouseUp(input.Left)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "mouseup" })

	err = input.Scroll(0, -3)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "wheel" && e.Num("dy") > 0 })

	// Focus the text field, then type.
	err = input.MoveMouse(wx+100, wy+tb+132)
	if err != nil {
		t.Fatal(err)
	}
	err = input.Click(input.Left)
	if err != nil {
		t.Fatal(err)
	}
	err = input.KeyTap(input.KeyA, input.ModShift)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "key" && e.Str("key") == "A" })
	const s = "é👋"
	err = input.TypeString(s)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "input" && e.Str("value") == "A"+s })
}
