//go:build hwtest_global && darwin

// Capture reads the display as the user sees it, so testwin must be in front:
// this brings it forward. Run only in a VM or on a machine nobody is using.
//
//	go test -tags hwtest_global -run Global ./screen
package screen_test

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/crgimenes/native/internal/testwin"
	"github.com/crgimenes/native/screen"
)

func TestGlobalCapture(t *testing.T) {
	if !screen.CaptureAllowed() {
		t.Fatal("this process lacks the Screen Recording permission")
	}
	const wx, wy = 100, 100
	w := testwin.Start(t, wx, wy, "-front")
	tb := int(w.Seen()[0].Num("titlebar"))
	time.Sleep(500 * time.Millisecond)

	// The swatch is page (0,0)-(200,100); take the middle of it, in points.
	r := image.Rect(wx+50, wy+tb+25, wx+150, wy+tb+75)
	img, err := screen.Capture(r)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < r.Dx() || img.Bounds().Dy() < r.Dy() {
		t.Fatalf("captured %v for a %v-point rectangle", img.Bounds(), r.Size())
	}
	c := img.RGBAAt(img.Bounds().Dx()/2, img.Bounds().Dy()/2)
	if !near(c, color.RGBA{0xff, 0x00, 0xff, 0xff}) {
		t.Errorf("centre pixel %v, want magenta", c)
	}
}
