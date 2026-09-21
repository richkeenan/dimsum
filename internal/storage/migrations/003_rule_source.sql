-- Legacy descriptions are not a trustworthy source identity.
ALTER TABLE rule_versions ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
CREATE INDEX rule_versions_source ON rule_versions(source_id,boot_id,generation,rule_id);
PRAGMA user_version=3;
