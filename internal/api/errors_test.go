package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestWriteJSONMarshalFailureFallsBackTo500 exercises writeJSON's guard for a
// value json.Marshal cannot encode (a channel): the handler must still emit a
// well-formed error envelope rather than a partial or missing body.
func TestWriteJSONMarshalFailureFallsBackTo500(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, struct {
		Ch chan int `json:"ch"`
	}{Ch: make(chan int)})

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var resp errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != errCodeInternal {
		t.Errorf("error = %q, want %q", resp.Error, errCodeInternal)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
