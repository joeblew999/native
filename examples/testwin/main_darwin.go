// Command testwin is the target the input and screen hardware tests drive. It
// is a glaze window with a text field, a click counter and a solid colour
// swatch, run as its own process so the tests reach it the way they would
// reach any other application: by PID and window ID only.
//
// By default it never activates. The app runs with the Prohibited activation
// policy, so macOS will not bring it forward even when a synthesized click
// lands on it, and the window is ordered to the back of the window list at
// launch. Whoever is using the Mac keeps their frontmost app and their focus.
//
// -front reverses that, for the global-input tests only (they post into the
// system event stream, so the window must be the one with focus); those run
// in a VM, never on a desktop someone is using.
//
// Everything it sees is written to stdout as one JSON object per line:
//
//	{"type":"start","pid":123,"window":456,"titlebar":28}
//	{"type":"ready"}
//	{"type":"key","key":"a","code":"KeyA","mods":["shift"]}
//	{"type":"input","value":"hello"}
//	{"type":"mousedown","x":10,"y":20,"button":0}   (and "mouseup")
//	{"type":"click","x":10,"y":20,"button":0,"count":1}
//	{"type":"wheel","dx":0,"dy":-3}
//	{"type":"state","active":false,"key":false,"visible":true,"exposed":true}
//	{"type":"covered", ...the state fields}   (with -cover)
//
// Page coordinates count from the top-left of the web content, below the
// title bar. -nsevents adds a line for every NSEvent the app receives, before
// any window sees it: the tool that found how to route a posted click.
//
// The swatch is magenta while the text field is empty and cyan once it holds
// text.
//
// It exits when stdin closes, so a test that dies does not leave it behind.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/crgimenes/glaze"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func init() { runtime.LockOSThread() }

type cgPoint struct{ X, Y float64 }
type cgSize struct{ W, H float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

const (
	// NSApplicationActivationPolicyProhibited: no Dock icon, and the app can
	// never become active, so no click or key can front it.
	activationPolicyProhibited = 2
	activationPolicyRegular    = 0

	styleTitled          = 1 << 0
	backingStoreBuffered = 2

	winW, winH = 400, 300
)

// Swatch is the solid region the capture tests look for, in CSS pixels from
// the top-left of the web content.
const page = `<!doctype html>
<html><head><style>
html,body{margin:0;height:100%;background:#ffffff;font:14px -apple-system,sans-serif;overflow:hidden}
#swatch{position:absolute;left:0;top:0;width:200px;height:100px;background:#ff00ff}
#field{position:absolute;left:10px;top:120px;width:360px;height:24px}
#btn{position:absolute;left:10px;top:160px;width:160px;height:60px}
#scroll{position:absolute;left:200px;top:160px;width:180px;height:120px;overflow:scroll;background:#eeeeee}
</style></head><body>
<div id="swatch"></div>
<input id="field" autofocus>
<button id="btn">clicks: 0</button>
<div id="scroll"><div style="height:2000px;width:2000px"></div></div>
<script>
(function(){
  var field = document.getElementById('field');
  var btn = document.getElementById('btn');
  var clicks = 0;
  function mods(e){
    var m = [];
    if (e.shiftKey) m.push('shift');
    if (e.ctrlKey) m.push('control');
    if (e.altKey) m.push('option');
    if (e.metaKey) m.push('command');
    return m;
  }
  document.addEventListener('keydown', function(e){
    report({type:'key', key:e.key, code:e.code, mods:mods(e)});
  }, true);
  var swatch = document.getElementById('swatch');
  field.addEventListener('input', function(){
    // The swatch turns cyan while the field holds text, so a capture can see
    // that background typing changed the page.
    swatch.style.background = field.value ? '#00ffff' : '#ff00ff';
    report({type:'input', value:field.value});
  });
  document.addEventListener('mousedown', function(e){
    report({type:'mousedown', x:e.clientX, y:e.clientY, button:e.button});
  }, true);
  document.addEventListener('mouseup', function(e){
    report({type:'mouseup', x:e.clientX, y:e.clientY, button:e.button});
  }, true);
  document.addEventListener('click', function(e){
    if (e.target === btn) { clicks++; btn.textContent = 'clicks: ' + clicks; }
    report({type:'click', x:e.clientX, y:e.clientY, button:e.button, count:clicks});
  }, true);
  document.addEventListener('contextmenu', function(e){
    e.preventDefault();
    report({type:'click', x:e.clientX, y:e.clientY, button:e.button, count:clicks});
  }, true);
  document.addEventListener('wheel', function(e){
    report({type:'wheel', dx:e.deltaX, dy:e.deltaY});
  }, {capture:true, passive:true});
  field.focus();
  // Not requestAnimationFrame: WebKit stops it for an occluded window, and
  // this one sits behind everything. The capture test waits for the swatch.
  setTimeout(function(){ report({type:'ready'}); }, 100);
})();
</script></body></html>`

var out = struct {
	sync.Mutex
	enc *json.Encoder
}{enc: json.NewEncoder(os.Stdout)}

func emit(v any) {
	out.Lock()
	defer out.Unlock()
	_ = out.enc.Encode(v)
}

func main() {
	x := flag.Int("x", 80, "window left edge, in points from the left of the main screen")
	y := flag.Int("y", 80, "window top edge, in points from the top of the main screen")
	front := flag.Bool("front", false, "take the foreground instead: only for the hwtest_global tests, which drive the real cursor and keyboard and run in a VM")
	cover := flag.Bool("cover", false, "a second after start, cover the window completely with another opaque window of this process, and report \"covered\"")
	debug := flag.Bool("nsevents", false, "also report every NSEvent AppKit hands the app, before any window sees it")
	life := flag.Duration("timeout", 5*time.Minute, "exit after this long")
	flag.Parse()

	go func() {
		// Parent gone (stdin closed): leave with it.
		_, _ = io.Copy(io.Discard, bufio.NewReader(os.Stdin))
		os.Exit(0)
	}()
	go func() {
		time.Sleep(*life)
		os.Exit(0)
	}()

	_, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		fail(err)
	}
	sel := objc.RegisterName
	app := objc.ID(objc.GetClass("NSApplication")).Send(sel("sharedApplication"))
	policy := activationPolicyProhibited
	if *front {
		policy = activationPolicyRegular
	}
	app.Send(sel("setActivationPolicy:"), policy)

	// Cocoa's origin is the bottom-left of the main screen; flip the requested
	// top-left into it.
	screen := objc.ID(objc.GetClass("NSScreen")).Send(sel("mainScreen"))
	frame := objc.Send[cgRect](screen, sel("frame"))
	top := frame.Size.H - float64(*y)

	win := objc.ID(objc.GetClass("NSWindow")).Send(sel("alloc")).Send(
		sel("initWithContentRect:styleMask:backing:defer:"),
		cgRect{cgPoint{float64(*x), top - winH}, cgSize{winW, winH}},
		uint(styleTitled), uint(backingStoreBuffered), false)
	win.Send(sel("setTitle:"), objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), "native testwin"))
	win.Send(sel("setReleasedWhenClosed:"), false)
	// Behind every other window, and without making anything key or active.
	win.Send(sel("orderBack:"), objc.ID(0))
	if *front {
		win.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
		app.Send(sel("activateIgnoringOtherApps:"), true)
	}

	w, err := glaze.NewWithOptions(glaze.Options{
		Window:            ptr(uintptr(win)),
		AcceptsFirstMouse: true,
	})
	if err != nil {
		fail(err)
	}
	defer w.Destroy()

	err = w.Bind("report", func(v map[string]any) { emit(v) })
	if err != nil {
		fail(err)
	}
	emit(map[string]any{
		"type":   "start",
		"pid":    os.Getpid(),
		"window": objc.Send[int](win, sel("windowNumber")),
		// Height of the title bar in points: window-relative coordinates
		// count from the frame's top-left, the page from the content's.
		"titlebar": objc.Send[cgRect](win, sel("frame")).Size.H - winH,
	})
	if *debug {
		monitor(win)
	}
	w.SetHtml(page)
	go func() {
		time.Sleep(time.Second)
		w.Dispatch(func() { emit(state("state", app, win)) })
		if !*cover {
			return
		}
		w.Dispatch(func() { coverWith(win) })
		// Occlusion state is updated asynchronously by the window server.
		time.Sleep(700 * time.Millisecond)
		w.Dispatch(func() { emit(state("covered", app, win)) })
	}()
	w.Run()
}

func state(typ string, app, win objc.ID) map[string]any {
	sel := objc.RegisterName
	return map[string]any{
		"type":    typ,
		"active":  objc.Send[bool](app, sel("isActive")),
		"key":     objc.Send[bool](win, sel("isKeyWindow")),
		"visible": objc.Send[bool](win, sel("isVisible")),
		// NSWindowOcclusionStateVisible: false once nothing of it shows.
		"exposed": objc.Send[uint](win, sel("occlusionState"))&2 != 0,
	}
}

// coverWith puts an opaque green window exactly over win, directly above it
// in the window order, so win is hidden without anything moving to the front.
func coverWith(win objc.ID) {
	sel := objc.RegisterName
	const nsWindowAbove = 1
	frame := objc.Send[cgRect](win, sel("frame"))
	c := objc.ID(objc.GetClass("NSWindow")).Send(sel("alloc")).Send(
		sel("initWithContentRect:styleMask:backing:defer:"),
		frame, uint(0), uint(backingStoreBuffered), false)
	c.Send(sel("setReleasedWhenClosed:"), false)
	c.Send(sel("setBackgroundColor:"), objc.ID(objc.GetClass("NSColor")).Send(
		sel("colorWithSRGBRed:green:blue:alpha:"), 0.0, 0.5, 0.0, 1.0))
	c.Send(sel("orderWindow:relativeTo:"), nsWindowAbove, objc.Send[int](win, sel("windowNumber")))
}

// ptr reinterprets a native handle as the unsafe.Pointer glaze takes; spelled
// this way only to quiet go vet's unsafeptr check.
func ptr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u)) // #nosec G103 -- an NSWindow handle, not Go memory
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "testwin:", err)
	os.Exit(1)
}

// monitor reports each event at the NSApplication level, which tells a posted
// event that never reached the app apart from one AppKit routed nowhere.
func monitor(win objc.ID) {
	sel := objc.RegisterName
	block := objc.NewBlock(func(_ objc.Block, ev objc.ID) objc.ID {
		emit(map[string]any{
			"type":   "nsevent",
			"nstype": objc.Send[uint](ev, sel("type")),
			"window": objc.Send[int](ev, sel("windowNumber")),
			"haswin": objc.Send[objc.ID](ev, sel("window")) != 0,
			"ours":   objc.Send[int](win, sel("windowNumber")),
			"loc":    objc.Send[cgPoint](ev, sel("locationInWindow")),
		})
		return ev
	})
	objc.ID(objc.GetClass("NSEvent")).Send(sel("addLocalMonitorForEventsMatchingMask:handler:"), ^uint64(0), block)
}
