// Package api implements the v0.1 REST API: GET /attention, GET /connections,
// GET /sync-log, PUT/DELETE /items/{id}/flag, and the coarse SSE stream
// GET /events. A [Server] also satisfies engine.Notifier — its
// AttentionChanged/SyncCompleted/SyncFailed methods fan events out to the SSE
// clients — so cmd/devpit/main.go wires one instance as both the HTTP handler
// and the engine's notifier. Construct a [Server] with [New].
package api
