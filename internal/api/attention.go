package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/vilaca/devpit/internal/attention"
	"github.com/vilaca/devpit/internal/storage"
)

// attentionResponse is the GET /attention response envelope.
type attentionResponse struct {
	Items []attentionItem `json:"items"`
}

// jiraRef is the optional Jira context embedded in an attentionItem.
type jiraRef struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

// attentionItem is one entry in the GET /attention response.
type attentionItem struct {
	ID                    string               `json:"id"`
	ConnectionID          string               `json:"connection_id"`
	ConnectionLabel       string               `json:"connection_label"`
	ConnectionType        string               `json:"connection_type"`
	ObjectType            string               `json:"object_type"`
	NativeID              string               `json:"native_id"`
	Title                 string               `json:"title"`
	URL                   string               `json:"url"`
	Repo                  string               `json:"repo"`
	Author                string               `json:"author"`
	Draft                 bool                 `json:"draft"`
	States                []attention.State    `json:"states"`
	Muted                 bool                 `json:"muted,omitempty"`
	Flagged               bool                 `json:"flagged"`
	Stale                 bool                 `json:"stale"`
	Old                   bool                 `json:"old"`
	UpdatedAt             time.Time            `json:"updated_at"`
	SignalCounts          map[string]int       `json:"signal_counts,omitempty"`
	FailingChecks         bool                 `json:"failing_checks"`
	MergeConflict         bool                 `json:"merge_conflict"`
	NeedsRebase           bool                 `json:"needs_rebase"`
	NeedsApproval         bool                 `json:"needs_approval"`
	UnresolvedDiscussions bool                 `json:"unresolved_discussions"`
	PolicyDenied          bool                 `json:"policy_denied"`
	ApprovalsCount        int                  `json:"approvals_count,omitempty"`
	MyReviewState         string               `json:"my_review_state,omitempty"`
	MyRoles               []string             `json:"my_roles,omitempty"`
	GateDetail            string               `json:"gate_detail,omitempty"`
	FlaggedAt             *time.Time           `json:"flagged_at,omitempty"`
	Since                 map[string]time.Time `json:"since,omitempty"`
	Labels                []string             `json:"labels,omitempty"`
	Jira                  *jiraRef             `json:"jira,omitempty"`
}

// handleAttention serves GET /attention. The optional ?state= query parameter
// filters the list to items whose States slice contains the given state value.
func (s *Server) handleAttention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := attention.List(ctx, s.db, s.connectionIDs(), time.Now(), s.staleThres, s.oldThres)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "failed to load attention list")
		return
	}

	jiraTickets := s.fetchJiraTickets(ctx, items)

	stateFilter := attention.State(r.URL.Query().Get("state"))

	result := make([]attentionItem, 0, len(items))
	for _, it := range items {
		if stateFilter != "" && !hasState(it, stateFilter) {
			continue
		}
		result = append(result, toAttentionItem(it, s.connByID[it.ConnectionID], jiraTickets))
	}

	writeJSON(w, http.StatusOK, attentionResponse{Items: result})
}

// fetchJiraTickets collects the union of TicketKeys across all items and does
// a single bulk read from jira_tickets. Returns nil on error or no keys.
func (s *Server) fetchJiraTickets(ctx context.Context, items []attention.WorkItem) map[string]storage.JiraTicket {
	seen := map[string]bool{}
	var keys []string
	for _, it := range items {
		for _, k := range it.TicketKeys {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	tickets, err := s.db.GetJiraTickets(ctx, keys)
	if err != nil {
		return nil
	}
	return tickets
}

// hasState reports whether item carries the given state.
func hasState(it attention.WorkItem, state attention.State) bool {
	return slices.Contains(it.States, state)
}

// toAttentionItem maps a WorkItem and its ConnectionMeta to the wire shape.
// jiraTickets is the bulk-fetched jira_tickets cache (may be nil).
func toAttentionItem(
	it attention.WorkItem, meta ConnectionMeta, jiraTickets map[string]storage.JiraTicket,
) attentionItem {
	ai := attentionItem{
		ID:                    it.ID,
		ConnectionID:          it.ConnectionID,
		ConnectionLabel:       meta.Label,
		ConnectionType:        meta.Type,
		ObjectType:            it.ObjectType,
		NativeID:              it.NativeID,
		Title:                 it.Title,
		URL:                   it.URL,
		Repo:                  it.Repo,
		Author:                it.Author,
		Draft:                 it.Draft,
		States:                it.States,
		Muted:                 it.Muted,
		Flagged:               it.Flagged,
		Stale:                 it.Stale,
		Old:                   it.Old,
		UpdatedAt:             it.UpdatedAt,
		SignalCounts:          it.SignalCounts,
		FailingChecks:         it.FailingChecks,
		MergeConflict:         it.MergeConflict,
		NeedsRebase:           it.NeedsRebase,
		NeedsApproval:         it.NeedsApproval,
		UnresolvedDiscussions: it.UnresolvedDiscussions,
		PolicyDenied:          it.PolicyDenied,
		ApprovalsCount:        it.ApprovalsCount,
		MyReviewState:         it.MyReviewState,
		MyRoles:               it.MyRoles,
		GateDetail:            it.GateDetail,
		FlaggedAt:             it.FlaggedAt,
		Since:                 it.Since,
		Labels:                it.Labels,
	}
	// Decorate with the first ticket key that has a cached row with a non-empty status.
	for _, key := range it.TicketKeys {
		if t, ok := jiraTickets[key]; ok && t.Status != "" {
			ai.Jira = &jiraRef{Key: key, Status: t.Status, URL: t.URL}
			break
		}
	}
	return ai
}
