//go:build hwtest && darwin

// Hardware tests: capture examples/testwin, a separate process whose window
// sits behind every other window (and, in one test, is fully covered), by its
// window ID alone, and look for the solid swatch it paints. The frontmost
// application must not change.
//
//	go test -tags hwtest ./screen
package screen_test

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crgimenes/native/input"
	"github.com/crgimenes/native/internal/testwin"
	"github.com/crgimenes/native/screen"
)

func TestCaptureWindow(t *testing.T) {
	if !screen.CaptureAllowed() {
		t.Fatal("this process lacks the Screen Recording permission; grant it to the terminal running the tests, this suite never works around it")
	}
	front := testwin.Frontmost(t)
	w := testwin.Start(t, 160, 180)
	tb := w.Seen()[0].Num("titlebar")

	// The page has drawn a frame by "ready", but the window server may not
	// have composited it yet, so give the swatch a moment to show up.
	var img *image.RGBA
	var err error
	for range 20 {
		img, err = screen.CaptureWindow(w.Window)
		if err != nil {
			t.Fatal(err)
		}
		if swatchShows(img, tb) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	b := img.Bounds()
	scale := float64(b.Dx()) / 400 // testwin's content is 400 points wide
	if scale < 1 || float64(b.Dy()) < (300+tb)*scale-2 {
		t.Fatalf("captured %v, want 400x%v points at a scale >= 1", b, 300+tb)
	}
	t.Logf("captured %v (scale %.2f)", b, scale)
	if os.Getenv("SCREEN_KEEP") != "" {
		saveFor(t, img)
	}

	at := func(x, y float64) color.RGBA { return img.RGBAAt(int(x*scale), int((tb+y)*scale)) }
	// The swatch spans page (0,0)-(200,100); sample its middle, away from
	// edges that a scale might blur.
	if c := at(100, 50); !near(c, color.RGBA{0xff, 0x00, 0xff, 0xff}) {
		t.Errorf("swatch pixel %v, want magenta", c)
	}
	if c := at(300, 60); !near(c, color.RGBA{0xff, 0xff, 0xff, 0xff}) {
		t.Errorf("background pixel %v, want white", c)
	}

	if after := testwin.Frontmost(t); after != front {
		t.Errorf("frontmost app changed: %q -> %q", front, after)
	}
}

// TestCaptureWindowCovered hides testwin completely under another window,
// types into it in the background, and expects the capture to show the page's
// reaction (the swatch turns cyan): fresh content from a window nobody can
// see.
func TestCaptureWindowCovered(t *testing.T) {
	if !screen.CaptureAllowed() || !input.Trusted() {
		t.Fatal("needs both the Screen Recording and the Accessibility permission")
	}
	front := testwin.Frontmost(t)
	w := testwin.Start(t, 200, 220, "-cover")
	tb := w.Seen()[0].Num("titlebar")
	c := w.Wait(t, 10*time.Second, func(e testwin.Event) bool { return e.Type() == "covered" })
	if c["exposed"] != false {
		t.Fatalf("testwin still partly visible after covering: %v", c)
	}

	err := input.Target(w.PID).TypeString("x")
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "input" && e.Str("value") == "x" })

	cyan := color.RGBA{0x00, 0xff, 0xff, 0xff}
	var got color.RGBA
	for range 20 {
		img, err := screen.CaptureWindow(w.Window)
		if err != nil {
			t.Fatal(err)
		}
		scale := float64(img.Bounds().Dx()) / 400
		got = img.RGBAAt(int(100*scale), int((tb+50)*scale))
		if near(got, cyan) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !near(got, cyan) {
		t.Errorf("covered window's swatch is %v after typing, want cyan: the capture did not see the update", got)
	}
	if after := testwin.Frontmost(t); after != front {
		t.Errorf("frontmost app changed: %q -> %q", front, after)
	}
}

func TestCaptureWindowMissing(t *testing.T) {
	if !screen.CaptureAllowed() {
		t.Fatal("no Screen Recording permission")
	}
	_, err := screen.CaptureWindow(0xfffffff0)
	if !errors.Is(err, screen.ErrNoWindow) {
		t.Fatalf("err %v, want ErrNoWindow", err)
	}
}

func swatchShows(img *image.RGBA, tb float64) bool {
	scale := float64(img.Bounds().Dx()) / 400
	return near(img.RGBAAt(int(100*scale), int((tb+50)*scale)), color.RGBA{0xff, 0x00, 0xff, 0xff})
}

// saveFor writes the capture where SCREEN_KEEP points, to look at by eye.
func saveFor(t *testing.T, img *image.RGBA) {
	path := filepath.Join(os.Getenv("SCREEN_KEEP"), "testwin.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, img)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Log("saved", path)
}
