package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "tok"})
	return c
}

func TestClientFetch200(t *testing.T) {
	body := `{"fields":{"summary":"Fix it","status":{"name":"In Review"},"assignee":{"displayName":"Alice"}}}`
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	result, fetchErr, err := c.Fetch(context.Background(), "RPC-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if fetchErr != "" {
		t.Fatalf("fetchErr = %q, want empty", fetchErr)
	}
	if result.Status != "In Review" {
		t.Errorf("Status = %q, want In Review", result.Status)
	}
	if result.Summary != "Fix it" {
		t.Errorf("Summary = %q, want Fix it", result.Summary)
	}
	if result.Assignee != "Alice" {
		t.Errorf("Assignee = %q, want Alice", result.Assignee)
	}
	if result.URL == "" {
		t.Error("URL should not be empty")
	}
}

func TestClientFetch200NoAssignee(t *testing.T) {
	body := `{"fields":{"summary":"No one","status":{"name":"Open"},"assignee":null}}`
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	result, fetchErr, err := c.Fetch(context.Background(), "RPC-2")
	if err != nil || fetchErr != "" {
		t.Fatalf("Fetch: err=%v fetchErr=%q", err, fetchErr)
	}
	if result.Assignee != "" {
		t.Errorf("Assignee = %q, want empty for null assignee", result.Assignee)
	}
}

func TestClientFetch404(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, fetchErr, err := c.Fetch(context.Background(), "GONE-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if fetchErr != "not found" {
		t.Errorf("fetchErr = %q, want %q", fetchErr, "not found")
	}
}

func TestClientFetch500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, fetchErr, err := c.Fetch(context.Background(), "ERR-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if fetchErr != "status 500" {
		t.Errorf("fetchErr = %q, want %q", fetchErr, "status 500")
	}
}

func TestClientAuthHeader(t *testing.T) {
	var gotAuth string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"","status":{"name":""},"assignee":null}}`))
	}))

	if _, _, err := c.Fetch(context.Background(), "AUTH-1"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth == "" || gotAuth[:6] != "Basic " {
		t.Errorf("Authorization = %q, want Basic ...", gotAuth)
	}
}
