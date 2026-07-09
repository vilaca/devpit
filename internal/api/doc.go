// Package api implements the v0.1 REST API: GET /attention, GET /connections,
// GET /sync-log, and PUT/DELETE /items/{id}/flag. The SSE stream (GET /events)
// is Phase 2. Construct a [Server] with [New] and register it with the HTTP
// server in cmd/devpit/main.go.
package api
