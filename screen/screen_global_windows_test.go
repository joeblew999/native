//go:build hwtest_global && windows

// Capture reads the display as the user sees it, so testwin must be in front:
// this brings it forward. Run only in a VM or on a CI runner.
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
	w := testwin.Start(t, 100, 100, "-front")
	s := w.Seen()[0]
	cx, cy := int(s.Num("clientx")), int(s.Num("clienty"))
	time.Sleep(time.Second)

	// The swatch is page (0,0)-(200,100); take the middle of it.
	r := image.Rect(cx+50, cy+25, cx+150, cy+75)
	img, err := screen.Capture(r)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != r.Dx() || img.Bounds().Dy() != r.Dy() {
		t.Fatalf("captured %v for a %v rectangle", img.Bounds(), r.Size())
	}
	c := img.RGBAAt(img.Bounds().Dx()/2, img.Bounds().Dy()/2)
	if !near(c, color.RGBA{0xff, 0x00, 0xff, 0xff}) {
		t.Errorf("centre pixel %v, want magenta", c)
	}
}
