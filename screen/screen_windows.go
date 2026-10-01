// Windows backend: GDI. CaptureWindow asks the window to render itself into a
// memory bitmap with PrintWindow(PW_RENDERFULLCONTENT), which goes through the
// DWM redirection surface, so it sees the composed content (DirectComposition
// included, which is how WebView2 draws) of a window that is covered or in the
// background. Capture is a BitBlt from the screen DC; Size is GetSystemMetrics.
//
// Plain stdcalls through syscall's lazy DLLs, the x/sys pattern; nothing
// crosses by value, and the DIB is read where GDI allocated it.

package screen

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")

	procIsWindow         = user32.NewProc("IsWindow")
	procIsIconic         = user32.NewProc("IsIconic")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procPrintWindow      = user32.NewProc("PrintWindow")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procGdiFlush           = gdi32.NewProc("GdiFlush")

	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	pwRenderFullContent = 0x00000002
	smCXScreen          = 0
	smCYScreen          = 1
	srcCopy             = 0x00CC0020
	captureBlt          = 0x40000000
	dibRGBColors        = 0
	dwmwaExtFrame       = 9 // DWMWA_EXTENDED_FRAME_BOUNDS
)

type rect struct{ Left, Top, Right, Bottom int32 }

// bitmapInfoHeader is BITMAPINFOHEADER; with BI_RGB and 32 bits per pixel no
// colour table follows, so it stands in for the whole BITMAPINFO.
type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

func call(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

// ptr reinterprets an address that native code returned; see the README's
// note on go vet's unsafeptr check.
func ptr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u)) // #nosec G103 -- native address, not Go memory
}

func captureAllowed() bool { return true }

func size() (int, int, error) {
	w := int(call(procGetSystemMetrics, smCXScreen))
	h := int(call(procGetSystemMetrics, smCYScreen))
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("screen: GetSystemMetrics reports %dx%d", w, h)
	}
	return w, h, nil
}

// dib is a top-down 32-bit memory bitmap selected into its own DC.
type dib struct {
	dc, bmp, old uintptr
	bits         uintptr
	w, h         int
}

func newDIB(w, h int) (*dib, error) {
	screenDC := call(procGetDC, 0)
	if screenDC == 0 {
		return nil, fmt.Errorf("screen: GetDC failed")
	}
	defer call(procReleaseDC, 0, screenDC)
	d := &dib{w: w, h: h}
	d.dc = call(procCreateCompatibleDC, screenDC)
	if d.dc == 0 {
		return nil, fmt.Errorf("screen: CreateCompatibleDC failed")
	}
	bi := bitmapInfoHeader{
		width:    int32(w),  // #nosec G115 -- window sizes
		height:   -int32(h), // #nosec G115 -- negative: top-down rows
		planes:   1,
		bitCount: 32,
	}
	bi.size = uint32(unsafe.Sizeof(bi))
	d.bmp = call(procCreateDIBSection, d.dc, uintptr(unsafe.Pointer(&bi)), dibRGBColors, uintptr(unsafe.Pointer(&d.bits)), 0, 0)
	if d.bmp == 0 || d.bits == 0 {
		call(procDeleteDC, d.dc)
		return nil, fmt.Errorf("screen: CreateDIBSection %dx%d failed", w, h)
	}
	d.old = call(procSelectObject, d.dc, d.bmp)
	return d, nil
}

func (d *dib) close() {
	call(procSelectObject, d.dc, d.old)
	call(procDeleteObject, d.bmp)
	call(procDeleteDC, d.dc)
}

// image copies the sub-rectangle r of the bitmap out as RGBA. GDI leaves
// alpha undefined (0 for most content), so it is set opaque; the BGRA bytes
// are swapped into RGBA order.
func (d *dib) image(r image.Rectangle) *image.RGBA {
	call(procGdiFlush)
	src := unsafe.Slice((*byte)(ptr(d.bits)), d.w*d.h*4)
	img := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := range r.Dy() {
		s := src[((r.Min.Y+y)*d.w+r.Min.X)*4:]
		o := img.Pix[y*img.Stride:]
		for x := range r.Dx() {
			o[x*4+0] = s[x*4+2]
			o[x*4+1] = s[x*4+1]
			o[x*4+2] = s[x*4+0]
			o[x*4+3] = 0xff
		}
	}
	return img
}

// blank reports whether every pixel is black or nearly so: what PrintWindow
// hands back for a window it could not read (protected with
// SetWindowDisplayAffinity, or drawn by a path DWM has no surface for) instead
// of failing.
func blank(img *image.RGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 8 || img.Pix[i+1] > 8 || img.Pix[i+2] > 8 {
			return false
		}
	}
	return true
}

func captureWindow(id uint32) (*image.RGBA, error) {
	// User handles are 32-bit values on every Windows, the documented
	// guarantee that lets 32- and 64-bit processes share HWNDs, so an HWND
	// fits the API's uint32.
	hwnd := uintptr(id)
	if id == 0 || call(procIsWindow, hwnd) == 0 {
		return nil, ErrNoWindow
	}
	if call(procIsIconic, hwnd) != 0 {
		return nil, fmt.Errorf("screen: window %#x is minimised and has no content to capture", id)
	}
	var wr rect
	if call(procGetWindowRect, hwnd, uintptr(unsafe.Pointer(&wr))) == 0 {
		return nil, ErrNoWindow
	}
	w, h := int(wr.Right-wr.Left), int(wr.Bottom-wr.Top)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("screen: window %#x has no area", id)
	}
	d, err := newDIB(w, h)
	if err != nil {
		return nil, err
	}
	defer d.close()
	if call(procPrintWindow, hwnd, d.dc, pwRenderFullContent) == 0 {
		return nil, fmt.Errorf("screen: PrintWindow failed for window %#x", id)
	}
	// Crop the invisible resize borders GetWindowRect counts (Windows 10 and
	// later) down to the frame DWM draws: the window as the user sees it,
	// title bar included, no shadow.
	crop := image.Rect(0, 0, w, h)
	var fr rect
	if call(procDwmGetWindowAttribute, hwnd, dwmwaExtFrame, uintptr(unsafe.Pointer(&fr)), unsafe.Sizeof(fr)) == 0 && fr.Right > fr.Left {
		crop = image.Rect(int(fr.Left-wr.Left), int(fr.Top-wr.Top), int(fr.Right-wr.Left), int(fr.Bottom-wr.Top)).Intersect(crop)
	}
	img := d.image(crop)
	if blank(img) {
		return nil, fmt.Errorf("%w: PrintWindow returned an all-black image for window %#x", ErrBlank, id)
	}
	return img, nil
}

func capture(r image.Rectangle) (*image.RGBA, error) {
	sw, sh, err := size()
	if err != nil {
		return nil, err
	}
	r = r.Intersect(image.Rect(0, 0, sw, sh))
	if r.Empty() {
		return nil, fmt.Errorf("screen: rectangle is outside the main display")
	}
	d, err := newDIB(r.Dx(), r.Dy())
	if err != nil {
		return nil, err
	}
	defer d.close()
	screenDC := call(procGetDC, 0)
	if screenDC == 0 {
		return nil, fmt.Errorf("screen: GetDC failed")
	}
	defer call(procReleaseDC, 0, screenDC)
	// CAPTUREBLT includes layered windows, as the user sees them.
	if call(procBitBlt, d.dc, 0, 0, uintptr(r.Dx()), uintptr(r.Dy()), screenDC, uintptr(r.Min.X), uintptr(r.Min.Y), srcCopy|captureBlt) == 0 {
		return nil, fmt.Errorf("screen: BitBlt failed")
	}
	return d.image(image.Rect(0, 0, r.Dx(), r.Dy())), nil
}
