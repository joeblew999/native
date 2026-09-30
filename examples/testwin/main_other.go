//go:build !darwin

// testwin is macOS-only for now: the input and screen backends it exercises
// are macOS-only.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "testwin: macOS only")
	os.Exit(1)
}
