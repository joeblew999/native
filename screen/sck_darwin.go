// ScreenCaptureKit, kept on its own because it is the one asynchronous part:
// every call takes an Objective-C completion block that runs later on a
// dispatch queue thread. Each wait below turns one block into a Go channel
// receive with a deadline.
//
// Block lifetime follows the README's ABI discipline: the block is released
// only after it has run. If the deadline passes first it is deliberately
// leaked, since ScreenCaptureKit may still call it.

package screen

import (
	"errors"
	"fmt"
	"image"
	"time"

	"github.com/ebitengine/purego/objc"
)

// sckTimeout bounds each asynchronous call. A capture takes tens of
// milliseconds; anything near this means ScreenCaptureKit is stuck.
const sckTimeout = 10 * time.Second

type result struct {
	obj uintptr // a retained SCShareableContent or CGImage, or 0
	err error
}

// wait sends msg to receiver with a completion block appended as the last
// argument, and blocks until the block runs. keep retains the block's object
// argument so it outlives the block (ownership passes to the caller).
func wait(receiver objc.ID, msg string, keep func(uintptr) uintptr, args ...any) (uintptr, error) {
	done := make(chan result, 1)
	block := objc.NewBlock(func(_ objc.Block, obj uintptr, nserr objc.ID) {
		if nserr != 0 {
			done <- result{err: fmt.Errorf("screen: %s", goString(nserr.Send(sel("localizedDescription"))))}
			return
		}
		if obj == 0 {
			done <- result{err: errors.New("screen: " + msg + " returned nothing")}
			return
		}
		done <- result{obj: keep(obj)}
	})
	receiver.Send(sel(msg), append(args, block)...)
	select {
	case r := <-done:
		block.Release()
		return r.obj, r.err
	case <-time.After(sckTimeout):
		return 0, fmt.Errorf("screen: %s timed out after %v", msg, sckTimeout)
	}
}

func retainObject(o uintptr) uintptr {
	objc.ID(o).Send(sel("retain"))
	return o
}

// shareableContent lists every window and display, on screen or not.
func shareableContent() (objc.ID, error) {
	c, err := wait(class("SCShareableContent"),
		"getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:",
		retainObject, false, false)
	return objc.ID(c), err
}

// screenshot captures filter at the filter's full pixel resolution.
func screenshot(filter objc.ID, pointW, pointH float64) (*image.RGBA, error) {
	scale := float64(objc.Send[float32](filter, sel("pointPixelScale")))
	if scale <= 0 {
		scale = 1
	}
	config := class("SCStreamConfiguration").Send(sel("new"))
	defer config.Send(sel("release"))
	config.Send(sel("setWidth:"), uint(pointW*scale))
	config.Send(sel("setHeight:"), uint(pointH*scale))
	config.Send(sel("setShowsCursor:"), false)
	if config.Send(sel("respondsToSelector:"), sel("setIgnoreShadowsSingleWindow:")) != 0 {
		config.Send(sel("setIgnoreShadowsSingleWindow:"), true)
	}
	img, err := wait(class("SCScreenshotManager"),
		"captureImageWithFilter:configuration:completionHandler:",
		cgImageRetain, filter, config)
	if err != nil {
		return nil, err
	}
	defer cgImageRelease(img)
	return rgba(img)
}

func sckCaptureWindow(id uint32) (*image.RGBA, error) {
	content, err := shareableContent()
	if err != nil {
		return nil, err
	}
	defer content.Send(sel("release"))
	windows := content.Send(sel("windows"))
	var win objc.ID
	for i := range objc.Send[uint](windows, sel("count")) {
		w := windows.Send(sel("objectAtIndex:"), i)
		if objc.Send[uint32](w, sel("windowID")) == id {
			win = w
			break
		}
	}
	if win == 0 {
		return nil, fmt.Errorf("%w: %d", ErrNoWindow, id)
	}
	frame := objc.Send[cgRect](win, sel("frame"))
	filter := class("SCContentFilter").Send(sel("alloc")).Send(sel("initWithDesktopIndependentWindow:"), win)
	defer filter.Send(sel("release"))
	return screenshot(filter, frame.W, frame.H)
}

// sckCaptureDisplay captures a whole display and reports its pixels per point.
func sckCaptureDisplay(displayID uint32) (*image.RGBA, float64, error) {
	content, err := shareableContent()
	if err != nil {
		return nil, 0, err
	}
	defer content.Send(sel("release"))
	displays := content.Send(sel("displays"))
	var display objc.ID
	for i := range objc.Send[uint](displays, sel("count")) {
		d := displays.Send(sel("objectAtIndex:"), i)
		if objc.Send[uint32](d, sel("displayID")) == displayID {
			display = d
			break
		}
	}
	if display == 0 {
		return nil, 0, fmt.Errorf("screen: display %d not shareable", displayID)
	}
	w := float64(objc.Send[int](display, sel("width")))
	h := float64(objc.Send[int](display, sel("height")))
	filter := class("SCContentFilter").Send(sel("alloc")).Send(sel("initWithDisplay:excludingWindows:"),
		display, class("NSArray").Send(sel("array")))
	defer filter.Send(sel("release"))
	img, err := screenshot(filter, w, h)
	if err != nil {
		return nil, 0, err
	}
	return img, float64(img.Bounds().Dx()) / w, nil
}
