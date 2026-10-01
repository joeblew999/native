//go:build !darwin && !windows

// testwin exists where the input and screen backends do: macOS and Windows.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "testwin: macOS and Windows only")
	os.Exit(1)
}
