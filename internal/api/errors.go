package api

import (
	"encoding/json"
	"net/http"
)

// Error codes used in the uniform error envelope (docs/REST_API.md Conventions).
const (
	errCodeNotFound   = "not_found"
	errCodeBadRequest = "bad_request"
	errCodeInternal   = "internal"
)

// errorResponse is the uniform error envelope: {"error": code, "message": text}.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeJSON marshals v to JSON, sets Content-Type, then writes status and the
// body. Marshaling happens before WriteHeader so encoding failures can still
// produce a 500 rather than a partial response with the wrong status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal","message":"response encoding failed"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeError writes a uniform error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: code, Message: message})
}
