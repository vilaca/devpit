package api

import "net/http"

// handleFlagSet serves PUT /items/{id}/flag — pins the item in the "Handle
// next" zone. Returns 204 No Content on success.
func (s *Server) handleFlagSet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "missing item id")
		return
	}
	if err := s.db.SetHandleNext(r.Context(), id, true); err != nil {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "failed to set flag")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFlagClear serves DELETE /items/{id}/flag — removes the "Handle next"
// pin. Returns 204 No Content whether the item was flagged or not.
func (s *Server) handleFlagClear(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "missing item id")
		return
	}
	if err := s.db.SetHandleNext(r.Context(), id, false); err != nil {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "failed to clear flag")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
