package api

import (
	"net/http"
	"time"

	"github.com/vilaca/devpit/internal/storage"
)

// syncLogResponse is the GET /sync-log response envelope.
type syncLogResponse struct {
	Entries []syncLogEntry `json:"entries"`
}

// syncLogEntry is one cycle row in the GET /sync-log response.
type syncLogEntry struct {
	ID              int64      `json:"id"`
	ConnectionID    string     `json:"connection_id"`
	ConnectionLabel string     `json:"connection_label"`
	Ts              time.Time  `json:"ts"`
	Operation       string     `json:"operation"`
	Outcome         string     `json:"outcome"`
	ItemsChanged    int        `json:"items_changed"`
	RateRemaining   *int       `json:"rate_remaining"`
	Retries         int        `json:"retries"`
	NextRetry       *time.Time `json:"next_retry"`
	Error           *string    `json:"error"`
}

// handleSyncLog serves GET /sync-log. The optional ?connection= query parameter
// filters to a single connection (used by the sync-activity banner deep-link).
func (s *Server) handleSyncLog(w http.ResponseWriter, r *http.Request) {
	connID := r.URL.Query().Get("connection")
	rows, err := s.db.ReadSyncLog(r.Context(), connID, syncLogReadLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "failed to read sync log")
		return
	}

	result := make([]syncLogEntry, len(rows))
	for i, row := range rows {
		result[i] = toSyncLogEntry(row, s.labelFor(row.ConnectionID))
	}
	writeJSON(w, http.StatusOK, syncLogResponse{Entries: result})
}

// toSyncLogEntry maps a storage.SyncLogEntry to the wire shape, adding the
// denormalized connection_label so the log remains readable after a connection
// is removed from config.
func toSyncLogEntry(e storage.SyncLogEntry, label string) syncLogEntry {
	return syncLogEntry{
		ID:              e.ID,
		ConnectionID:    e.ConnectionID,
		ConnectionLabel: label,
		Ts:              e.Ts,
		Operation:       e.Operation,
		Outcome:         e.Outcome,
		ItemsChanged:    e.ItemsChanged,
		RateRemaining:   e.RateRemaining,
		Retries:         e.Retries,
		NextRetry:       e.NextRetry,
		Error:           e.Error,
	}
}
