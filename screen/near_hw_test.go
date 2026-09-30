//go:build (hwtest || hwtest_global) && darwin

package screen_test

import "image/color"

// near allows for colour conversion rounding between the page's sRGB and the
// display's colour space and back.
func near(a, b color.RGBA) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) <= 6 && d(a.G, b.G) <= 6 && d(a.B, b.B) <= 6 && d(a.A, b.A) <= 6
}
