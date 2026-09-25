-- Retain timestamp/id ordering for query-log pagination while covering device
-- counts, blocked totals and last-seen times without loading response payloads.
DROP INDEX events_client_time;
CREATE INDEX events_client_time ON query_events(client_id,timestamp,id,outcome);
PRAGMA user_version=7;
