package tools

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// resetStdinPump detaches the process-wide stdin pump so each test can bind a
// fresh os.Stdin pipe. Production code starts the pump exactly once.
func resetStdinPump() {
	stdinPumpOnce = sync.Once{}
	stdinMu.Lock()
	stdinLines = nil
	stdinMu.Unlock()
}

// Verifies that AskUser consumes terminal input through the single shared pump
// (one reader for the process lifetime) rather than spawning a scanner per call.
func TestAskUserReadsFromSharedStdinPump(t *testing.T) {
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() { os.Stdin = orig; r.Close(); w.Close() }()

	resetStdinPump()
	startStdinPump()

	if _, err := w.WriteString("hello from terminal\n"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := AskUser(ctx, map[string]interface{}{"question": "test?"})
	if got != "hello from terminal" {
		t.Fatalf("got %q, want %q", got, "hello from terminal")
	}
}

// A closed stdin must not make AskUser spin or panic; it should fall back to
// waiting for the web reply / deadline.
func TestAskUserHandlesClosedStdin(t *testing.T) {
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	w.Close() // EOF immediately
	defer func() { os.Stdin = orig; r.Close() }()

	resetStdinPump()
	startStdinPump()
	// Give the pump a moment to observe EOF and close the channel.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := AskUser(ctx, map[string]interface{}{"question": "test?"})
	if got != "Timed out waiting for user response" {
		t.Fatalf("got %q", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("returned too slowly (%v), possible busy loop", elapsed)
	}
}
