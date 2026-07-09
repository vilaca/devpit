// Package attention implements the attention fold: it reads the event log and
// produces the ranked WorkItem list with attention-state tags.
//
// The fold is a pure function over stored events: there is no materialized
// state. For each provider object it takes the latest item.observed snapshot
// as the source of facts, folds those facts into the six attention states,
// and derives the ranking timestamp, staleness, and per-signal tag counts
// from the raw signal events. Items are ranked by fixed state precedence
// then newest-first. See docs/Attention_Engine.md and
// docs/Event_Taxonomy_and_Storage.md for the design.
//
// Enrichment that needs data outside the event log — connection label/type, and
// the "Handle next" pin (flagged) that lifts items into a separate zone — is
// applied by the API layer, which owns the config and the handle_next table.
package attention
