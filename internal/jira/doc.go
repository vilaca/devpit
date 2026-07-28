// Package jira enriches work items with Jira Cloud ticket status (ADR-0021).
// The Client fetches individual ticket data via the Jira REST API; the
// Refresher runs a background loop that keeps the jira_tickets storage table
// current and notifies the SSE hub when data changes.
package jira
