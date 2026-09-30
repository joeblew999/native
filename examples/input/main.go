// Command input demonstrates github.com/crgimenes/native/input. With no flags
// it only reports whether the process holds the Accessibility permission.
// Given an application's PID it types into that application in the
// background, leaving the cursor and the frontmost app alone:
//
//	go run ./examples/input
//	go run ./examples/input -pid 1234 -type "hello 👋"
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/crgimenes/native/input"
)

func main() {
	pid := flag.Int("pid", 0, "process to type into (0: just report the permission)")
	text := flag.String("type", "hello from native/input", "text to type")
	flag.Parse()

	fmt.Println("accessibility permission:", input.Trusted())
	if *pid == 0 {
		return
	}
	err := input.Target(*pid).TypeString(*text)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("typed %q into pid %d\n", *text, *pid)
}
