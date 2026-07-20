package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSE tuning. These are protocol properties, not user config.
const (
	// sseKeepAlive is how often an idle stream emits a comment line, keeping the
	// connection alive through idle proxies and surfacing dead sockets.
	sseKeepAlive = 30 * time.Second
	// sseClientBuffer is the per-client send-queue depth. Events are coarse and
	// idempotent — each only tells the client to re-fetch — so when a slow
	// client fills its buffer we drop the overflow rather than block the engine;
	// the next frame it receives brings it fully up to date (docs/REST_API.md).
	sseClientBuffer = 16
)

// hub is the SSE fan-out. The engine calls the broadcast path from its poll
// goroutines while HTTP handlers register and drop clients concurrently, so
// every access to clients is guarded by mu. Safe for concurrent use.
type hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newHub() *hub {
	return &hub{clients: make(map[chan []byte]struct{})}
}

// register adds a client and returns its frame channel. The caller
// (handleEvents) owns the lifecycle and must pair this with unregister.
func (h *hub) register() chan []byte {
	ch := make(chan []byte, sseClientBuffer)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// unregister removes a client and closes its channel. It is idempotent so a
// double defer (loop exit + panic) cannot double-close.
func (h *hub) unregister(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// broadcast fans a pre-formatted SSE frame out to every client without ever
// blocking: a client whose buffer is full is skipped (see sseClientBuffer).
// Holding mu here serialises against unregister's close, so a send never races
// a close.
func (h *hub) broadcast(frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- frame:
		default: // slow client — drop this frame; it re-syncs on the next one
		}
	}
}

// connEvent is the payload for the per-connection events. Coarse by design: it
// carries only the connection id (so the client knows what to re-fetch) and,
// for failures, the plain-language cause that drives the banner — never the
// changed domain data itself (docs/REST_API.md).
type connEvent struct {
	ConnectionID string `json:"connection_id"`
	Cause        string `json:"cause,omitempty"`
}

// sseFrame formats one named event as an SSE frame: an "event:" line, a "data:"
// line, and the terminating blank line.
func sseFrame(event string, data []byte) []byte {
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", event, data)
}

// AttentionChanged, SyncCompleted, and SyncFailed satisfy engine.Notifier
// (checked in cmd/devpit/main.go, which imports both packages — internal/api
// must not import internal/engine). Each just fans a coarse frame out to the
// connected SSE clients.

// AttentionChanged tells every client the ranked list may have changed.
func (s *Server) AttentionChanged() {
	s.hub.broadcast(sseFrame("attention.changed", []byte("{}")))
}

// SyncCompleted reports that a poll cycle finished cleanly for one connection.
func (s *Server) SyncCompleted(connID string) {
	s.broadcastJSON("sync.completed", connEvent{ConnectionID: connID})
}

// SyncFailed reports that a poll cycle failed for one connection; cause is
// plain-language banner text.
func (s *Server) SyncFailed(connID, cause string) {
	s.broadcastJSON("sync.failed", connEvent{ConnectionID: connID, Cause: cause})
}

// broadcastJSON marshals a coarse event payload and fans it out. Payloads are
// string-only, so Marshal cannot fail; the guard exists only to keep the
// impossible error from producing a malformed frame.
func (s *Server) broadcastJSON(event string, payload connEvent) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.hub.broadcast(sseFrame(event, data))
}

// handleEvents serves GET /events: the one-directional SSE stream. It registers
// the client, then relays frames until the request context is cancelled — by
// the client disconnecting or by the server shutting down (main wires the root
// context in via http.Server.BaseContext).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.hub.register()
	defer s.hub.unregister(ch)

	keepAlive := time.NewTicker(sseKeepAlive)
	defer keepAlive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-ch:
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
