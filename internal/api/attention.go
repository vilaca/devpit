package api

import (
	"net/http"
	"slices"
	"time"

	"github.com/vilaca/devpit/internal/attention"
)

// attentionResponse is the GET /attention response envelope.
type attentionResponse struct {
	Items []attentionItem `json:"items"`
}

// attentionItem is one entry in the GET /attention response.
type attentionItem struct {
	ID              string               `json:"id"`
	ConnectionID    string               `json:"connection_id"`
	ConnectionLabel string               `json:"connection_label"`
	ConnectionType  string               `json:"connection_type"`
	ObjectType      string               `json:"object_type"`
	NativeID        string               `json:"native_id"`
	Title           string               `json:"title"`
	URL             string               `json:"url"`
	Repo            string               `json:"repo"`
	Author          string               `json:"author"`
	Draft           bool                 `json:"draft"`
	States          []attention.State    `json:"states"`
	Flagged         bool                 `json:"flagged"`
	Stale           bool                 `json:"stale"`
	Old             bool                 `json:"old"`
	UpdatedAt       time.Time            `json:"updated_at"`
	SignalCounts    map[string]int       `json:"signal_counts,omitempty"`
	FailingChecks   bool                 `json:"failing_checks"`
	MergeConflict   bool                 `json:"merge_conflict"`
	NeedsRebase     bool                 `json:"needs_rebase"`
	GateDetail      string               `json:"gate_detail,omitempty"`
	FlaggedAt       *time.Time           `json:"flagged_at,omitempty"`
	Since           map[string]time.Time `json:"since,omitempty"`
}

// handleAttention serves GET /attention. The optional ?state= query parameter
// filters the list to items whose States slice contains the given state value.
func (s *Server) handleAttention(w http.ResponseWriter, r *http.Request) {
	items, err := attention.List(r.Context(), s.db, s.connectionIDs(), time.Now(), s.staleThres, s.oldThres)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "failed to load attention list")
		return
	}

	stateFilter := attention.State(r.URL.Query().Get("state"))

	result := make([]attentionItem, 0, len(items))
	for _, it := range items {
		if stateFilter != "" && !hasState(it, stateFilter) {
			continue
		}
		result = append(result, toAttentionItem(it, s.connByID[it.ConnectionID]))
	}

	writeJSON(w, http.StatusOK, attentionResponse{Items: result})
}

// hasState reports whether item carries the given state.
func hasState(it attention.WorkItem, state attention.State) bool {
	return slices.Contains(it.States, state)
}

// toAttentionItem maps a WorkItem and its ConnectionMeta to the wire shape.
func toAttentionItem(it attention.WorkItem, meta ConnectionMeta) attentionItem {
	return attentionItem{
		ID:              it.ID,
		ConnectionID:    it.ConnectionID,
		ConnectionLabel: meta.Label,
		ConnectionType:  meta.Type,
		ObjectType:      it.ObjectType,
		NativeID:        it.NativeID,
		Title:           it.Title,
		URL:             it.URL,
		Repo:            it.Repo,
		Author:          it.Author,
		Draft:           it.Draft,
		States:          it.States,
		Flagged:         it.Flagged,
		Stale:           it.Stale,
		Old:             it.Old,
		UpdatedAt:       it.UpdatedAt,
		SignalCounts:    it.SignalCounts,
		FailingChecks:   it.FailingChecks,
		MergeConflict:   it.MergeConflict,
		NeedsRebase:     it.NeedsRebase,
		GateDetail:      it.GateDetail,
		FlaggedAt:       it.FlaggedAt,
		Since:           it.Since,
	}
}
