// Package engine drives the poll/sync lifecycle: schedules FastPoll and
// Reconcile calls, writes events and sync_log rows, and manages provider
// goroutines.
package engine
