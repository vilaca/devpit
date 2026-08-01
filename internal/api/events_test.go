package api

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHubFanOut checks that a broadcast reaches every registered client and
// that the frame carries the expected event name and payload.
func TestHubFanOut(t *testing.T) {
	s := newTestServer(t, openTestDB(t))

	a := s.hub.register()
	b := s.hub.register()
	defer s.hub.unregister(a)
	defer s.hub.unregister(b)

	s.SyncCompleted("gh")

	for name, ch := range map[string]chan []byte{"a": a, "b": b} {
		select {
		case frame := <-ch:
			got := string(frame)
			if !strings.Contains(got, "event: sync.completed\n") {
				t.Errorf("client %s: missing event name in %q", name, got)
			}
			if !strings.Contains(got, `"connection_id":"gh"`) {
				t.Errorf("client %s: missing connection_id in %q", name, got)
			}
		default:
			t.Errorf("client %s received no frame", name)
		}
	}
}

// TestHubDropsSlowClient checks that a client whose buffer is full does not
// block the broadcaster: overflow frames are dropped, earlier ones survive.
func TestHubDropsSlowClient(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	ch := s.hub.register()
	defer s.hub.unregister(ch)

	// Emit more events than the buffer holds; broadcast must never block.
	done := make(chan struct{})
	go func() {
		for range sseClientBuffer * 2 {
			s.AttentionChanged()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on a full client buffer")
	}

	if got := len(ch); got != sseClientBuffer {
		t.Errorf("buffered frames = %d, want %d (buffer cap)", got, sseClientBuffer)
	}
}

// TestHubUnregisterIsIdempotent guards the double-defer path in handleEvents.
func TestHubUnregisterIsIdempotent(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	ch := s.hub.register()
	s.hub.unregister(ch)
	s.hub.unregister(ch) // must not panic on the already-closed channel
}

// TestEventsStreamEndToEnd runs the /events handler over a real HTTP connection
// and confirms a Notifier call is delivered as an SSE frame, then that
// cancelling the request context ends the handler.
func TestEventsStreamEndToEnd(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	ts := httptest.NewServer(s)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// The client is registered only once handleEvents runs; poll until it is.
	waitForClients(t, s, 1)
	s.SyncFailed("gh", "rate limited")

	frame := readFrame(t, resp.Body)
	if !strings.Contains(frame, "event: sync.failed") {
		t.Errorf("frame missing event name: %q", frame)
	}
	if !strings.Contains(frame, `"cause":"rate limited"`) {
		t.Errorf("frame missing cause: %q", frame)
	}

	// Cancelling the request context must unwind the handler and drop the client.
	cancel()
	waitForClients(t, s, 0)
}

// nonFlushingWriter wraps an http.ResponseWriter without exposing Flush, so it
// deliberately does not satisfy http.Flusher — used to exercise handleEvents's
// "streaming unsupported" guard, which httptest.ResponseRecorder can't reach
// since it implements Flusher itself.
type nonFlushingWriter struct {
	http.ResponseWriter
}

// TestHandleEventsFlusherUnsupported checks that a ResponseWriter lacking
// http.Flusher gets a 500 instead of a hung/panicking stream.
func TestHandleEventsFlusherUnsupported(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	rec := httptest.NewRecorder()
	w := nonFlushingWriter{ResponseWriter: rec}
	r := httptest.NewRequestWithContext(context.Background(), "GET", "/events", nil)

	s.handleEvents(w, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), errCodeInternal) {
		t.Errorf("body = %q, want it to contain %q", rec.Body.String(), errCodeInternal)
	}
}

// readFrame reads lines until the blank-line frame terminator.
func readFrame(t *testing.T, r io.Reader) string {
	t.Helper()
	sc := bufio.NewScanner(r)
	var b strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			return b.String()
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	t.Fatalf("stream ended before a full frame: %v", sc.Err())
	return ""
}

// waitForClients polls the hub until it holds want clients, or fails.
func waitForClients(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.hub.mu.Lock()
		n := len(s.hub.clients)
		s.hub.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hub client count never reached %d", want)
}
