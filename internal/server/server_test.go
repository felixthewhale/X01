package server

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func postReply(t *testing.T, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/reply", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleReply(rec, req)
	return rec.Code
}

func TestReplyDeliveredViaHTTP(t *testing.T) {
	ch := RequestReply("who are you?")
	defer ClearRequest()

	code := postReply(t, `{"content":"I am X01"}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	select {
	case got := <-ch:
		if got != "I am X01" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reply was not delivered")
	}
}

func TestReplyAfterClearIsRejectedNotBlocked(t *testing.T) {
	RequestReply("q")
	ClearRequest() // request ended before the human replied

	done := make(chan int, 1)
	go func() { done <- postReply(t, `{"content":"too late"}`) }()

	select {
	case code := <-done:
		if code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("late reply blocked the handler (leak)")
	}
}

func TestSecondReplyForSameRequestIsConflict(t *testing.T) {
	ch := RequestReply("q")
	defer ClearRequest()

	if code := postReply(t, `{"content":"first"}`); code != http.StatusOK {
		t.Fatalf("first reply: expected 200, got %d", code)
	}
	<-ch
	if code := postReply(t, `{"content":"second"}`); code != http.StatusConflict {
		t.Fatalf("second reply: expected 409, got %d", code)
	}
}

// Simulates many ask_user round trips; the old unbuffered, shared replyChan would
// block handlers when a request ended between the activeRequest check and the send.
func TestRepeatedRequestReplyCyclesDoNotLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 500; i++ {
		ch := RequestReply("q")
		if code := postReply(t, `{"content":"r"}`); code != http.StatusOK {
			t.Fatalf("cycle %d: expected 200, got %d", i, code)
		}
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("cycle %d: reply not delivered", i)
		}
		ClearRequest()
	}

	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+10 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestClearRequestClosesChannel(t *testing.T) {
	ch := RequestReply("q")
	ClearRequest()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after ClearRequest")
		}
	case <-time.After(time.Second):
		t.Fatal("channel was not closed")
	}
}
