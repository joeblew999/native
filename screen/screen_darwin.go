// macOS backend: the permission check, the display size and CGImage
// conversion. The ScreenCaptureKit calls, asynchronous and block-based, live
// apart in sck_darwin.go.

package screen

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type cgRect struct {
	X, Y, W, H float64
}

const (
	// kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big: bytes R, G, B,
	// A in memory, premultiplied, which is image.RGBA's layout exactly.
	bitmapRGBAPremultiplied = 1 | 4<<12
)

var (
	cgPreflightScreenCaptureAccess func() bool
	cgMainDisplayID                func() uint32
	cgDisplayPixelsWide            func(display uint32) uint
	cgDisplayPixelsHigh            func(display uint32) uint
	cgImageGetWidth                func(img uintptr) uint
	cgImageGetHeight               func(img uintptr) uint
	cgImageRetain                  func(img uintptr) uintptr
	cgImageRelease                 func(img uintptr)
	cgColorSpaceCreateWithName     func(name uintptr) uintptr
	cgColorSpaceRelease            func(cs uintptr)
	cgBitmapContextCreate          func(data uintptr, w, h, bitsPerComponent, bytesPerRow uint, cs uintptr, info uint32) uintptr
	cgBitmapContextGetData         func(ctx uintptr) uintptr
	cgContextDrawImage             func(ctx uintptr, r cgRect, img uintptr)
	cgContextRelease               func(ctx uintptr)

	colorSpaceSRGB uintptr
)

var load = sync.OnceValue(func() error {
	for _, fw := range []string{
		"/System/Library/Frameworks/Foundation.framework/Foundation",
		"/System/Library/Frameworks/ScreenCaptureKit.framework/ScreenCaptureKit",
	} {
		_, err := purego.Dlopen(fw, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return fmt.Errorf("screen: %w", err)
		}
	}
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("screen: %w", err)
	}
	purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, cg, "CGPreflightScreenCaptureAccess")
	purego.RegisterLibFunc(&cgMainDisplayID, cg, "CGMainDisplayID")
	purego.RegisterLibFunc(&cgDisplayPixelsWide, cg, "CGDisplayPixelsWide")
	purego.RegisterLibFunc(&cgDisplayPixelsHigh, cg, "CGDisplayPixelsHigh")
	purego.RegisterLibFunc(&cgImageGetWidth, cg, "CGImageGetWidth")
	purego.RegisterLibFunc(&cgImageGetHeight, cg, "CGImageGetHeight")
	purego.RegisterLibFunc(&cgImageRetain, cg, "CGImageRetain")
	purego.RegisterLibFunc(&cgImageRelease, cg, "CGImageRelease")
	purego.RegisterLibFunc(&cgColorSpaceCreateWithName, cg, "CGColorSpaceCreateWithName")
	purego.RegisterLibFunc(&cgColorSpaceRelease, cg, "CGColorSpaceRelease")
	purego.RegisterLibFunc(&cgBitmapContextCreate, cg, "CGBitmapContextCreate")
	purego.RegisterLibFunc(&cgBitmapContextGetData, cg, "CGBitmapContextGetData")
	purego.RegisterLibFunc(&cgContextDrawImage, cg, "CGContextDrawImage")
	purego.RegisterLibFunc(&cgContextRelease, cg, "CGContextRelease")
	// A CFStringRef variable; dlsym gives its address.
	addr, err := purego.Dlsym(cg, "kCGColorSpaceSRGB")
	if err != nil {
		return fmt.Errorf("screen: %w", err)
	}
	colorSpaceSRGB = *(*uintptr)(ptr(addr))
	// Opens this process's window-server connection. ScreenCaptureKit
	// assumes one: in a bare process (a CLI, a test binary) SCContentFilter
	// otherwise aborts in CGS_REQUIRE_INIT.
	cgMainDisplayID()
	return nil
})

// ptr reinterprets an address native code returned; spelled this way only to
// quiet go vet's unsafeptr check (see the repository README).
func ptr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u)) // #nosec G103 -- native address, not Go memory
}

func captureAllowed() bool {
	if load() != nil {
		return false
	}
	return cgPreflightScreenCaptureAccess()
}

func size() (int, int, error) {
	err := load()
	if err != nil {
		return 0, 0, err
	}
	d := cgMainDisplayID()
	return int(cgDisplayPixelsWide(d)), int(cgDisplayPixelsHigh(d)), nil // #nosec G115 -- display dimensions
}

// ready loads the frameworks and refuses to capture without the permission.
func ready() error {
	err := load()
	if err != nil {
		return err
	}
	if !cgPreflightScreenCaptureAccess() {
		return ErrNotAllowed
	}
	return nil
}

func captureWindow(id uint32) (*image.RGBA, error) {
	err := ready()
	if err != nil {
		return nil, err
	}
	var img *image.RGBA
	autorelease(func() { img, err = sckCaptureWindow(id) })
	return img, err
}

func capture(r image.Rectangle) (*image.RGBA, error) {
	err := ready()
	if err != nil {
		return nil, err
	}
	var (
		full  *image.RGBA
		scale float64
	)
	autorelease(func() { full, scale, err = sckCaptureDisplay(cgMainDisplayID()) })
	if err != nil {
		return nil, err
	}
	px := image.Rect(
		int(float64(r.Min.X)*scale), int(float64(r.Min.Y)*scale),
		int(float64(r.Max.X)*scale), int(float64(r.Max.Y)*scale),
	).Intersect(full.Bounds())
	if px.Empty() {
		return nil, fmt.Errorf("screen: %v lies outside the main display", r)
	}
	out := image.NewRGBA(image.Rect(0, 0, px.Dx(), px.Dy()))
	for y := range px.Dy() {
		src := full.PixOffset(px.Min.X, px.Min.Y+y)
		copy(out.Pix[y*out.Stride:(y+1)*out.Stride], full.Pix[src:src+px.Dx()*4])
	}
	return out, nil
}

// rgba draws a CGImage into an sRGB, premultiplied RGBA bitmap that
// CoreGraphics owns, then copies it into Go memory. Drawing, rather than
// reading the image's bytes directly, converts whatever pixel format and
// colour space the capture came in.
func rgba(img uintptr) (*image.RGBA, error) {
	w, h := cgImageGetWidth(img), cgImageGetHeight(img)
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("screen: empty image")
	}
	cs := cgColorSpaceCreateWithName(colorSpaceSRGB)
	if cs == 0 {
		return nil, fmt.Errorf("screen: no sRGB colour space")
	}
	defer cgColorSpaceRelease(cs)
	ctx := cgBitmapContextCreate(0, w, h, 8, w*4, cs, bitmapRGBAPremultiplied)
	if ctx == 0 {
		return nil, fmt.Errorf("screen: CGBitmapContextCreate failed for %dx%d", w, h)
	}
	defer cgContextRelease(ctx)
	cgContextDrawImage(ctx, cgRect{0, 0, float64(w), float64(h)}, img)
	data := cgBitmapContextGetData(ctx)
	if data == 0 {
		return nil, fmt.Errorf("screen: bitmap has no data")
	}
	out := image.NewRGBA(image.Rect(0, 0, int(w), int(h))) // #nosec G115 -- image dimensions
	copy(out.Pix, unsafe.Slice((*byte)(ptr(data)), len(out.Pix)))
	return out, nil
}

var (
	selMu    sync.Mutex
	selCache = map[string]objc.SEL{}
)

func sel(name string) objc.SEL {
	selMu.Lock()
	defer selMu.Unlock()
	s, ok := selCache[name]
	if !ok {
		s = objc.RegisterName(name)
		selCache[name] = s
	}
	return s
}

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

// autorelease runs f inside an NSAutoreleasePool on one OS thread: the pool
// is thread-local, so the goroutine must not migrate before the drain.
func autorelease(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := class("NSAutoreleasePool").Send(sel("alloc")).Send(sel("init"))
	defer pool.Send(sel("drain"))
	f()
}

// goString copies an NSString into Go.
func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	p := s.Send(sel("UTF8String"))
	if p == 0 {
		return ""
	}
	b := (*byte)(ptr(uintptr(p)))
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(b), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(b, n))
}
