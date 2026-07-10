package api

import (
	"net/http"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/internal/web"
)

// failureWindowMinutes is the rolling window used to compute connection health.
// Fixed at 60 minutes per ADR-0018; not user-configurable.
const failureWindowMinutes = 60

// syncLogReadLimit caps how many sync_log rows GET /sync-log returns.
// The table is user-bounded (no automatic compaction), so this is a safety cap.
const syncLogReadLimit = 10_000

// ConnectionMeta carries the config-derived fields for one connection that are
// not stored in the event log. Pass one per connection to [New]; the API layer
// uses these to enrich responses without importing internal/config.
type ConnectionMeta struct {
	ID       string // opaque stable id, matches connection_id in the DB
	Type     string // "github" | "gitlab"
	BaseURL  string
	Label    string
	Identity string // resolved handle; empty string if not yet resolved
}

// Server is an [http.Handler] that serves the v0.1 REST API. Construct with
// [New]. It is safe to call ServeHTTP concurrently from multiple goroutines.
type Server struct {
	db             *storage.DB
	mux            *http.ServeMux
	hub            *hub
	conns          []ConnectionMeta
	connByID       map[string]ConnectionMeta
	staleThres     time.Duration
	abandonedThres time.Duration
}

// New constructs a Server. staleThreshold and abandonedThreshold are forwarded
// to the attention fold; pass attention.DefaultStaleThreshold and
// attention.DefaultAbandonedThreshold unless an override is needed.
func New(db *storage.DB, connections []ConnectionMeta, staleThreshold, abandonedThreshold time.Duration) *Server {
	s := &Server{
		db:             db,
		mux:            http.NewServeMux(),
		hub:            newHub(),
		conns:          connections,
		connByID:       make(map[string]ConnectionMeta, len(connections)),
		staleThres:     staleThreshold,
		abandonedThres: abandonedThreshold,
	}
	for _, c := range connections {
		s.connByID[c.ID] = c
	}
	s.mux.HandleFunc("GET /attention", s.handleAttention)
	s.mux.HandleFunc("GET /events", s.handleEvents)
	s.mux.HandleFunc("GET /connections", s.handleConnections)
	s.mux.HandleFunc("GET /sync-log", s.handleSyncLog)
	s.mux.HandleFunc("PUT /items/{id}/flag", s.handleFlagSet)
	s.mux.HandleFunc("DELETE /items/{id}/flag", s.handleFlagClear)
	// Catch-all: serve the embedded SPA (ADR-0010). The API patterns above are
	// more specific, so ServeMux routes them first; everything else — "/",
	// assets, and client routes — falls through to the static handler, which
	// serves index.html for unknown paths so a browser refresh works.
	s.mux.Handle("GET /", web.Handler())
	return s
}

// ServeHTTP implements [http.Handler].
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// connectionIDs returns the ordered list of connection IDs from the config.
func (s *Server) connectionIDs() []string {
	ids := make([]string, len(s.conns))
	for i, c := range s.conns {
		ids[i] = c.ID
	}
	return ids
}

// labelFor returns the user-visible label for a connection, falling back to
// the connection_id itself when the connection is no longer in the config
// (orphaned sync_log rows).
func (s *Server) labelFor(connectionID string) string {
	if meta, ok := s.connByID[connectionID]; ok {
		return meta.Label
	}
	return connectionID
}

// _ asserts that *Server satisfies http.Handler at compile time.
var _ http.Handler = (*Server)(nil)
