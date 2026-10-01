//go:build hwtest || hwtest_global

package testwin

import (
	"os/exec"
	"strings"
	"testing"
)

// Frontmost names the frontmost application, the thing these tests promise
// never to change.
func Frontmost(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("osascript", "-e",
		`tell application "System Events" to get name of first application process whose frontmost is true`).Output()
	if err != nil {
		t.Fatalf("osascript: %v", err)
	}
	return strings.TrimSpace(string(out))
}
