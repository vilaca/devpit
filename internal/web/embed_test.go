package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These assertions hold whether or not the SPA has been built into dist/: the
// shell (real index.html or the committed placeholder) is served for "/" and
// for unknown client routes, and a missing asset is a genuine 404. This is what
// makes a browser refresh on any route correct (ADR-0010).
func TestHandlerServesShellAndFallsBack(t *testing.T) {
	h := Handler()

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantHTML   bool
	}{
		{"root serves shell", "/", http.StatusOK, true},
		{"unknown client route falls back to shell", "/buckets/needs-review", http.StatusOK, true},
		{"missing asset is 404", "/assets/does-not-exist.js", http.StatusNotFound, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantHTML {
				if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
					t.Errorf("Content-Type = %q, want html", ct)
				}
				if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
					t.Errorf("Cache-Control = %q, want no-cache", cc)
				}
			}
		})
	}
}
