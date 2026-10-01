package input

import "testing"

// TestPointByValue proves the CGPoint-by-value ABI at run time: a point goes
// into CoreGraphics as an argument (CGEventCreateMouseEvent, CGEventSetLocation)
// and comes back as a return value (CGEventGetLocation). A wrong register
// assignment compiles clean and shows up here as garbage. Nothing is posted.
func TestPointByValue(t *testing.T) {
	err := load()
	if err != nil {
		t.Fatal(err)
	}
	want := cgPoint{123.5, -456.25}
	ev := cgEventCreateMouseEvent(0, eventMouseMoved, want, 0)
	if ev == 0 {
		t.Fatal("CGEventCreateMouseEvent returned NULL")
	}
	defer cfRelease(ev)
	got := cgEventGetLocation(ev)
	if got != want {
		t.Fatalf("created at %v, read back %v", want, got)
	}
	want = cgPoint{-7, 8192.75}
	cgEventSetLocation(ev, want)
	got = cgEventGetLocation(ev)
	if got != want {
		t.Fatalf("set to %v, read back %v", want, got)
	}
}

// TestScrollEventFields checks the non-variadic scroll constructor takes its
// wheel arguments in order. Nothing is posted.
func TestScrollEventFields(t *testing.T) {
	err := load()
	if err != nil {
		t.Fatal(err)
	}
	ev := cgEventCreateScrollWheelEvent2(0, scrollUnitLine, 2, -3, 5, 0)
	if ev == 0 {
		t.Fatal("CGEventCreateScrollWheelEvent2 returned NULL")
	}
	defer cfRelease(ev)
	const deltaAxis1, deltaAxis2 = 11, 12
	dy := cgEventGetIntegerValueField(ev, deltaAxis1)
	dx := cgEventGetIntegerValueField(ev, deltaAxis2)
	if dy != -3 || dx != 5 {
		t.Fatalf("axis1 %d axis2 %d, want -3 and 5", dy, dx)
	}
}

func TestKeycodesComplete(t *testing.T) {
	for k := KeyA; k <= KeyF12; k++ {
		_, ok := keycodes[k]
		if !ok {
			t.Errorf("Key %d has no macOS keycode", k)
		}
	}
}
