// Command screen demonstrates github.com/crgimenes/native/screen. With no
// flags it reports the main display's size and whether the process holds the
// Screen Recording permission. Given a window ID it saves that window, covered
// or not, as a PNG:
//
//	go run ./examples/screen
//	go run ./examples/screen -window 1234 -o window.png
package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"

	"github.com/crgimenes/native/screen"
)

func main() {
	id := flag.Uint("window", 0, "window ID (CGWindowID) to capture (0: just report)")
	out := flag.String("o", "window.png", "where to write the capture")
	flag.Parse()

	w, h, err := screen.Size()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("main display: %dx%d points\nscreen recording permission: %v\n", w, h, screen.CaptureAllowed())
	if *id == 0 {
		return
	}
	img, err := screen.CaptureWindow(uint32(*id)) // #nosec G115 -- a window ID from the command line
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	err = png.Encode(f, img)
	if err != nil {
		log.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%v)\n", *out, img.Bounds().Size())
}
