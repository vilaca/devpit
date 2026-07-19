# Data Model

Storage is events-first: the primary table is the event/signal log.
There is no materialized current-state table in v0.1 (see
docs/Design_Decisions.md §2–§3).

Normalized (provider-neutral) entities — derived by folding the event
log at read time: WorkItem, Review, Mention, Discussion, Pipeline,
Notification, Release, Repository, User.

WorkItem is a derived logical grouping, not a stored primary key. Its
key includes the connection: `(connection-id, object-type, native-id)`.

Local-only tables: "Handle next" flags (§5), sync log (§16), sync
cursors, app state. Connection config lives in the config file only
(per §1b/§15); the database stores opaque connection-id strings.

Event taxonomy, fold rules, and the column-level schema:
docs/Event_Taxonomy_and_Storage.md.
