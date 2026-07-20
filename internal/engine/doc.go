// Package engine is the write side of DevPit. It owns one goroutine per
// configured connection, drives the tiered poll loop, persists events +
// cursors + sync-log rows through a Store, and notifies the API layer so the
// SSE stream can fire.
//
// It computes no attention state itself — buckets are folded on read by
// internal/attention. See docs/Synchronization_Engine.md for the full
// implementation spec.
package engine
