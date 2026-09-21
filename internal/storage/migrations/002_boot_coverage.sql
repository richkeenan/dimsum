ALTER TABLE rule_versions RENAME TO rule_versions_v1;
CREATE TABLE rule_versions(boot_id TEXT NOT NULL,generation INTEGER NOT NULL,rule_id INTEGER NOT NULL,description TEXT NOT NULL,PRIMARY KEY(boot_id,generation,rule_id));
-- Legacy explanations can only be attributed when exactly one boot references
-- the old identity. Ambiguous explanations remain unavailable, never guessed.
INSERT INTO rule_versions SELECT MIN(e.boot_id),r.generation,r.rule_id,r.description FROM rule_versions_v1 r JOIN query_events e ON e.generation=r.generation AND e.rule_id=r.rule_id GROUP BY r.generation,r.rule_id HAVING COUNT(DISTINCT e.boot_id)=1;
UPDATE writer_state SET incomplete=1 WHERE boot_id IN (SELECT e.boot_id FROM query_events e JOIN rule_versions_v1 r ON r.generation=e.generation AND r.rule_id=e.rule_id WHERE NOT EXISTS(SELECT 1 FROM rule_versions n WHERE n.boot_id=e.boot_id AND n.generation=e.generation AND n.rule_id=e.rule_id));
DROP TABLE rule_versions_v1;
ALTER TABLE writer_state ADD COLUMN snapshot_sequence INTEGER NOT NULL DEFAULT 0;
ALTER TABLE writer_state ADD COLUMN coverage_start INTEGER;
ALTER TABLE writer_state ADD COLUMN coverage_end INTEGER;
UPDATE writer_state SET snapshot_sequence=COALESCE(json_extract(snapshot,'$.Sequence'),0);
CREATE INDEX events_rule ON query_events(boot_id,generation,rule_id);
INSERT INTO storage_meta(key,value) VALUES('retention_pending',0);
PRAGMA user_version=2;
