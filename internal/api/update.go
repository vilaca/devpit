package api

// SetUpdate records the latest self-update status and, when it changed, nudges
// connected clients with an update.available SSE frame so the TopBar chip
// appears without a reload. It satisfies update.Sink (declared in
// internal/update, so this package needs no dependency on it). Safe for
// concurrent use; the update checker calls it from its own goroutine.
func (s *Server) SetUpdate(available bool, latestVersion, releaseURL string, inContainer bool) {
	next := updateInfo{
		Available:     available,
		LatestVersion: latestVersion,
		ReleaseURL:    releaseURL,
		InContainer:   inContainer,
	}
	s.updateMu.Lock()
	changed := next != s.update
	s.update = next
	s.updateMu.Unlock()

	if changed {
		s.hub.broadcast(sseFrame("update.available", []byte("{}")))
	}
}

// currentUpdate returns the last recorded update status for GET /connections.
func (s *Server) currentUpdate() updateInfo {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	return s.update
}
