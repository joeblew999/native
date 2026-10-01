//go:build hwtest && windows

// Hardware tests on Windows: capture examples/testwin (glaze/WebView2, a
// separate process, never activated) by its HWND alone, covered and not, and
// check that a window excluded from capture comes back as ErrBlank rather
// than a black image. The foreground window must not change.
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

var (
	magenta = color.RGBA{0xff, 0x00, 0xff, 0xff}
	white   = color.RGBA{0xff, 0xff, 0xff, 0xff}
	cyan    = color.RGBA{0x00, 0xff, 0xff, 0xff}
)

// offsets returns the web content's top-left within the captured frame.
func offsets(w *testwin.Win) (int, int) {
	s := w.Seen()[0]
	return int(s.Num("border")), int(s.Num("titlebar"))
}

// captureUntil captures until the page pixel (px, py) shows want, or gives up
// after a few seconds and returns the last capture.
func captureUntil(t *testing.T, w *testwin.Win, px, py int, want color.RGBA) (*image.RGBA, color.RGBA) {
	t.Helper()
	bx, tb := offsets(w)
	var img *image.RGBA
	var got color.RGBA
	var err error
	for range 30 {
		img, err = screen.CaptureWindow(w.Window)
		if err != nil && !errors.Is(err, screen.ErrBlank) {
			t.Fatal(err)
		}
		if err == nil {
			got = img.RGBAAt(bx+px, tb+py)
			if near(got, want) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("still %v after retries", err)
	}
	return img, got
}

func TestCaptureWindow(t *testing.T) {
	front := testwin.Frontmost(t)
	w := testwin.Start(t, 160, 180)
	img, got := captureUntil(t, w, 100, 50, magenta)
	bx, tb := offsets(w)
	b := img.Bounds()
	t.Logf("captured %v; content at (%d,%d)", b, bx, tb)
	keep(t, img, "testwin.png")
	if b.Dx() < 400 || b.Dy() < 300+tb {
		t.Fatalf("captured %v, want at least 400x%d", b, 300+tb)
	}
	if !near(got, magenta) {
		t.Errorf("swatch pixel %v, want magenta", got)
	}
	if c := img.RGBAAt(bx+300, tb+60); !near(c, white) {
		t.Errorf("background pixel %v, want white", c)
	}
	checkForeground(t, front, w)
}

// TestCaptureWindowCovered hides testwin under another window, types into it
// in the background, and expects the capture to show the page's reaction.
func TestCaptureWindowCovered(t *testing.T) {
	front := testwin.Frontmost(t)
	w := testwin.Start(t, 200, 220, "-cover")
	c := w.Wait(t, 10*time.Second, func(e testwin.Event) bool { return e.Type() == "covered" })
	if c["exposed"] != false {
		t.Fatalf("testwin still exposed after covering: %v", c)
	}
	captureUntil(t, w, 100, 50, magenta)
	time.Sleep(time.Second)
	w.Drain()
	// Text only lands in a focused page (see input's README); a background
	// click on the field restores focus if the cold start lost it.
	app := input.Target(w.PID)
	bx, tb := offsets(w)
	for try := 1; !w.PageFocused() && try <= 5; try++ {
		t.Logf("page not focused: background click on the text field (%d)", try)
		err := app.Click(bx+100, tb+132, input.Left)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		w.Drain()
	}
	err := app.TypeString("x")
	if err != nil {
		t.Fatal(err)
	}
	w.Wait(t, 3*time.Second, func(e testwin.Event) bool { return e.Type() == "input" && e.Str("value") == "x" })
	img, got := captureUntil(t, w, 100, 50, cyan)
	keep(t, img, "testwin-covered.png")
	if !near(got, cyan) {
		t.Errorf("covered window's swatch is %v after typing, want cyan: the capture did not see the update", got)
	}
	checkForeground(t, front, w)
}

// TestCaptureWindowProtected is the negative control for the blank check: a
// window with WDA_MONITOR display affinity must come back as ErrBlank, never
// as a black image.
func TestCaptureWindowProtected(t *testing.T) {
	w := testwin.Start(t, 240, 260, "-protect")
	time.Sleep(time.Second)
	img, err := screen.CaptureWindow(w.Window)
	if err == nil {
		keep(t, img, "testwin-protected.png")
		t.Fatalf("captured a protected window (%v), want ErrBlank", img.Bounds())
	}
	if !errors.Is(err, screen.ErrBlank) {
		t.Fatalf("err %v, want ErrBlank", err)
	}
	t.Log(err)
}

func TestCaptureWindowMissing(t *testing.T) {
	_, err := screen.CaptureWindow(0xfffffff0)
	if !errors.Is(err, screen.ErrNoWindow) {
		t.Fatalf("err %v, want ErrNoWindow", err)
	}
}

// checkForeground fails the test if testwin took the foreground; a change to
// another process is the CI runner's own doing and only logged.
func checkForeground(t *testing.T, front string, w *testwin.Win) {
	t.Helper()
	after := testwin.Frontmost(t)
	if after == front {
		return
	}
	if testwin.ForegroundPID() == w.PID {
		t.Errorf("testwin took the foreground: %s -> %s", front, after)
		return
	}
	t.Logf("foreground changed, not to testwin: %s -> %s", front, after)
}

// keep writes the capture where SCREEN_KEEP points (CI uploads it).
func keep(t *testing.T, img *image.RGBA, name string) {
	dir := os.Getenv("SCREEN_KEEP")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
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
}
