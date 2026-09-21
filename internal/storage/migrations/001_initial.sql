CREATE TABLE domains(id INTEGER PRIMARY KEY, name BLOB NOT NULL UNIQUE);
CREATE TABLE clients(id INTEGER PRIMARY KEY, address BLOB NOT NULL UNIQUE);
CREATE TABLE rule_versions(generation INTEGER NOT NULL, rule_id INTEGER NOT NULL, description TEXT NOT NULL, PRIMARY KEY(generation,rule_id));
CREATE TABLE query_events(
 id INTEGER PRIMARY KEY AUTOINCREMENT, boot_id TEXT NOT NULL, sequence INTEGER NOT NULL,
 timestamp INTEGER NOT NULL, duration INTEGER NOT NULL, domain_id INTEGER NOT NULL REFERENCES domains(id),
 client_id INTEGER NOT NULL REFERENCES clients(id), qtype INTEGER NOT NULL, qclass INTEGER NOT NULL,
 outcome INTEGER NOT NULL, rcode INTEGER NOT NULL, upstream_id INTEGER NOT NULL,
 generation INTEGER NOT NULL, rule_id INTEGER NOT NULL, flags INTEGER NOT NULL, alias BLOB,
 UNIQUE(boot_id,sequence));
CREATE INDEX events_time ON query_events(timestamp,id);
CREATE INDEX events_client_time ON query_events(client_id,timestamp,id);
CREATE INDEX events_domain_time ON query_events(domain_id,timestamp,id);
CREATE TABLE rollups(
 resolution INTEGER NOT NULL, bucket INTEGER NOT NULL, outcome INTEGER NOT NULL,
 count INTEGER NOT NULL, duration INTEGER NOT NULL,
 h0 INTEGER NOT NULL,h1 INTEGER NOT NULL,h2 INTEGER NOT NULL,h3 INTEGER NOT NULL,h4 INTEGER NOT NULL,h5 INTEGER NOT NULL,h6 INTEGER NOT NULL,h7 INTEGER NOT NULL,
 PRIMARY KEY(resolution,bucket,outcome));
CREATE TABLE rankings_hour(bucket INTEGER NOT NULL, kind INTEGER NOT NULL, key BLOB NOT NULL, count INTEGER NOT NULL, PRIMARY KEY(bucket,kind,key));
CREATE TABLE writer_state(boot_id TEXT PRIMARY KEY, event_watermark INTEGER NOT NULL DEFAULT 0, snapshot_watermark INTEGER NOT NULL DEFAULT 0, snapshot BLOB, incomplete INTEGER NOT NULL DEFAULT 0, lost_details INTEGER NOT NULL DEFAULT 0);
CREATE TABLE storage_meta(key TEXT PRIMARY KEY, value INTEGER NOT NULL);
INSERT INTO storage_meta VALUES('detail_cutoff',0),('minute_cutoff',0),('hour_cutoff',0),('day_cutoff',0),('writer_losses',0),('last_write',0);
PRAGMA user_version=1;
