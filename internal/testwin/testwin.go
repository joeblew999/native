//go:build hwtest || hwtest_global

// Package testwin launches examples/testwin for the hardware tests and reads
// what it reports. Test-only: it is compiled only under the hwtest tags.
package testwin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Event is one JSON line testwin printed.
type Event map[string]any

// Type is the event's "type" field.
func (e Event) Type() string {
	s, _ := e["type"].(string)
	return s
}

// Num reads a numeric field.
func (e Event) Num(k string) float64 {
	f, _ := e[k].(float64)
	return f
}

// Str reads a string field.
func (e Event) Str(k string) string {
	s, _ := e[k].(string)
	return s
}

// Win is a running testwin.
type Win struct {
	PID    int
	Window uint32

	cmd    *exec.Cmd
	events chan Event
	mu     sync.Mutex
	seen   []Event
}

// Start builds and launches testwin with its window's top-left at (x, y)
// points and any extra flags, waits until its page is ready, and stops it when
// the test ends.
func Start(t *testing.T, x, y int, flags ...string) *Win {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("testwin runs on macOS and Windows only")
	}
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "examples", "testwin")
	bin := filepath.Join(t.TempDir(), "testwin")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build testwin: %v\n%s", err, out)
	}

	args := append([]string{"-x", fmt.Sprint(x), "-y", fmt.Sprint(y), "-timeout", "3m"}, flags...)
	cmd := exec.Command(bin, args...)
	stdin, err := cmd.StdinPipe() // kept open: testwin exits when it closes
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	w := &Win{cmd: cmd, events: make(chan Event, 1024)}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			w.events <- e
		}
		close(w.events)
	}()

	start := w.Wait(t, 20*time.Second, func(e Event) bool { return e.Type() == "start" })
	w.PID = int(start.Num("pid"))
	w.Window = uint32(start.Num("window"))
	w.Wait(t, 20*time.Second, func(e Event) bool { return e.Type() == "ready" })
	return w
}

// Wait returns the first event that matches, failing the test on timeout.
func (w *Win) Wait(t *testing.T, d time.Duration, match func(Event) bool) Event {
	t.Helper()
	e, ok := w.Next(d, match)
	if !ok {
		t.Fatalf("testwin: no matching event within %v; saw %v", d, w.Seen())
	}
	return e
}

// Next is Wait without failing: ok is false on timeout.
func (w *Win) Next(d time.Duration, match func(Event) bool) (Event, bool) {
	timeout := time.After(d)
	for {
		select {
		case e, ok := <-w.events:
			if !ok {
				return nil, false
			}
			w.mu.Lock()
			w.seen = append(w.seen, e)
			w.mu.Unlock()
			if match(e) {
				return e, true
			}
		case <-timeout:
			return nil, false
		}
	}
}

// Drain discards whatever arrived so far, so the next Wait sees only what
// follows.
func (w *Win) Drain() {
	for {
		select {
		case e := <-w.events:
			w.mu.Lock()
			w.seen = append(w.seen, e)
			w.mu.Unlock()
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

// Seen is every event consumed so far.
func (w *Win) Seen() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Event(nil), w.seen...)
}
