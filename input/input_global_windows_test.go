//go:build hwtest_global && windows

// Global-input tests on Windows. They move the real cursor, click, scroll and
// type into whatever has focus (SendInput), and bring testwin to the front to
// receive it: run them only in a VM or on a CI runner, never on a desktop
// someone is using.
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
	w := testwin.Start(t, 100, 100, "-front")
	s := w.Seen()[0]
	cx, cy := int(s.Num("clientx")), int(s.Num("clienty"))
	time.Sleep(time.Second) // let activation settle
	w.Drain()

	// The button's middle, page (90,190), on screen.
	bx, by := cx+90, cy+190
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
	err = input.MoveMouse(cx+100, cy+132)
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
	const str = "é👋"
	err = input.TypeString(str)
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "input" && e.Str("value") == "A"+str })
}
