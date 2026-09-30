package screen

import (
	"image/color"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// TestRectByValue proves the CGRect-by-value ABI at run time for objc_msgSend,
// the way sck_darwin.go reads -[SCWindow frame]: a 32-byte struct goes in as an
// argument and comes back as a return value, which on amd64 means
// objc_msgSend_stret. A wrong choice compiles clean and fails only here.
func TestRectByValue(t *testing.T) {
	err := load()
	if err != nil {
		t.Fatal(err)
	}
	want := cgRect{1.5, -2, 300.25, 4096}
	v := class("NSValue").Send(sel("valueWithRect:"), want)
	if v == 0 {
		t.Fatal("+[NSValue valueWithRect:] returned nil")
	}
	got := objc.Send[cgRect](v, sel("rectValue"))
	if got != want {
		t.Fatalf("rect in %v, out %v", want, got)
	}
}

// TestRGBA runs the CGImage conversion on an image drawn here, which also
// passes CGRect by value to C functions (CGContextFillRect, and
// CGContextDrawImage inside rgba). Needs no permission and captures nothing.
func TestRGBA(t *testing.T) {
	err := load()
	if err != nil {
		t.Fatal(err)
	}
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var (
		setFill     func(ctx uintptr, r, g, b, a float64)
		fillRect    func(ctx uintptr, r cgRect)
		createImage func(ctx uintptr) uintptr
	)
	purego.RegisterLibFunc(&setFill, cg, "CGContextSetRGBFillColor")
	purego.RegisterLibFunc(&fillRect, cg, "CGContextFillRect")
	purego.RegisterLibFunc(&createImage, cg, "CGBitmapContextCreateImage")

	cs := cgColorSpaceCreateWithName(colorSpaceSRGB)
	defer cgColorSpaceRelease(cs)
	ctx := cgBitmapContextCreate(0, 4, 2, 8, 16, cs, bitmapRGBAPremultiplied)
	if ctx == 0 {
		t.Fatal("CGBitmapContextCreate failed")
	}
	defer cgContextRelease(ctx)
	setFill(ctx, 1, 0, 1, 1)
	fillRect(ctx, cgRect{0, 0, 2, 2}) // the left half; the right stays clear
	img := createImage(ctx)
	if img == 0 {
		t.Fatal("CGBitmapContextCreateImage failed")
	}
	defer cgImageRelease(img)

	out, err := rgba(img)
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 4 || out.Bounds().Dy() != 2 {
		t.Fatalf("bounds %v, want 4x2", out.Bounds())
	}
	for y := range 2 {
		for x := range 4 {
			want := color.RGBA{}
			if x < 2 {
				want = color.RGBA{0xff, 0, 0xff, 0xff}
			}
			got := out.RGBAAt(x, y)
			if got != want {
				t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}
