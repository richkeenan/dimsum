-- NULL is legacy/missing capture, distinct from a captured empty DNS response.
ALTER TABLE query_events ADD COLUMN response BLOB;
PRAGMA user_version=5;
