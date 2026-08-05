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

// connectionsResponse is the GET /connections response envelope. The self-update
// hint rides here (rather than on a dedicated endpoint) because this is the
// status the frontend already polls; the update.available SSE event nudges a
// re-fetch when it changes (docs/REST_API.md).
type connectionsResponse struct {
	Connections []connectionItem `json:"connections"`
	Update      updateInfo       `json:"update"`
}

// updateInfo is the self-update hint (ADR-0023). Available is false until the
// update checker (internal/update) finds a newer release; the UI renders the
// TopBar chip only when Available, using InContainer to pick the upgrade hint.
type updateInfo struct {
	Available     bool   `json:"available"`
	LatestVersion string `json:"latest_version,omitempty"`
	ReleaseURL    string `json:"release_url,omitempty"`
	InContainer   bool   `json:"in_container"`
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

// healthInfo carries the health metrics for one connection.
type healthInfo struct {
	Status       string     `json:"status"` // ok | degraded | failing
	LastSyncedAt *time.Time `json:"last_synced_at"`
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
	writeJSON(w, http.StatusOK, connectionsResponse{
		Connections: result,
		Update:      s.currentUpdate(),
	})
}

// outcomeStatus maps a sync_log outcome to ok / degraded / failing.
func outcomeStatus(outcome string) string {
	switch outcome {
	case "ok":
		return healthOK
	case "degraded":
		return healthDegraded
	default:
		return healthFailing
	}
}

// worstStatus returns the more severe of two health status values.
func worstStatus(a, b string) string {
	rank := map[string]int{healthOK: 0, healthDegraded: 1, healthFailing: 2}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

// computeHealth derives the health status for one connection from the sync_log.
// The dot reflects the worst of each operation's latest outcome (ADR-0018).
func (s *Server) computeHealth(ctx context.Context, connID string) (healthInfo, error) {
	latest, err := s.db.LatestOutcomesPerOperation(ctx, connID)
	if err != nil {
		return healthInfo{}, err
	}

	lastSynced, err := s.db.LastSyncedAt(ctx, connID)
	if err != nil {
		return healthInfo{}, err
	}

	status := healthOK
	for _, row := range latest {
		status = worstStatus(status, outcomeStatus(row.Outcome))
	}

	var lastSyncedAt *time.Time
	if !lastSynced.IsZero() {
		lastSyncedAt = &lastSynced
	}

	return healthInfo{
		Status:       status,
		LastSyncedAt: lastSyncedAt,
	}, nil
}
