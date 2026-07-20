package api

import (
	"context"
	"net/http"
	"time"
)

// Health status values (ADR-0018).
const (
	healthOK       = "ok"
	healthDegraded = "degraded"
	healthFailing  = "failing"
)

// connectionsResponse is the GET /connections response envelope.
type connectionsResponse struct {
	Connections []connectionItem `json:"connections"`
}

// connectionItem is one entry in the GET /connections response.
type connectionItem struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	BaseURL  string     `json:"base_url"`
	Label    string     `json:"label"`
	Identity *string    `json:"identity"` // null while not yet resolved
	Health   healthInfo `json:"health"`
}

// healthInfo carries the rolling-window health metrics for one connection.
type healthInfo struct {
	Status               string     `json:"status"` // ok | degraded | failing
	LastSyncedAt         *time.Time `json:"last_synced_at"`
	FailureCount         int        `json:"failure_count"`
	FailureWindowMinutes int        `json:"failure_window_minutes"`
}

// handleConnections serves GET /connections.
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result := make([]connectionItem, 0, len(s.conns))
	for _, meta := range s.conns {
		health, err := s.computeHealth(ctx, meta.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, errCodeInternal, "failed to compute health")
			return
		}
		var identity *string
		if meta.Identity != "" {
			identity = &meta.Identity
		}
		result = append(result, connectionItem{
			ID:       meta.ID,
			Type:     meta.Type,
			BaseURL:  meta.BaseURL,
			Label:    meta.Label,
			Identity: identity,
			Health:   health,
		})
	}
	writeJSON(w, http.StatusOK, connectionsResponse{Connections: result})
}

// computeHealth derives the health status for one connection from the sync_log.
// It counts failure rows within the last failureWindowMinutes and classifies
// status as ok / degraded / failing (ADR-0018).
func (s *Server) computeHealth(ctx context.Context, connID string) (healthInfo, error) {
	window := time.Now().UTC().Add(-failureWindowMinutes * time.Minute)
	rows, err := s.db.ReadSyncLogSince(ctx, connID, window)
	if err != nil {
		return healthInfo{}, err
	}

	lastSynced, err := s.db.LastSyncedAt(ctx, connID)
	if err != nil {
		return healthInfo{}, err
	}

	failureCount := 0
	for _, row := range rows {
		if row.Outcome != "ok" {
			failureCount++
		}
	}

	status := healthOK
	switch {
	case len(rows) > 0 && failureCount == len(rows):
		status = healthFailing
	case failureCount > 0:
		status = healthDegraded
	}

	var lastSyncedAt *time.Time
	if !lastSynced.IsZero() {
		lastSyncedAt = &lastSynced
	}

	return healthInfo{
		Status:               status,
		LastSyncedAt:         lastSyncedAt,
		FailureCount:         failureCount,
		FailureWindowMinutes: failureWindowMinutes,
	}, nil
}
