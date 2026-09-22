CREATE TABLE latency_bins(
 resolution INTEGER NOT NULL, bucket INTEGER NOT NULL, outcome INTEGER NOT NULL,
 bin INTEGER NOT NULL CHECK(bin >= 0 AND bin < 464), count INTEGER NOT NULL CHECK(count > 0),
 PRIMARY KEY(resolution,bucket,outcome,bin),
 FOREIGN KEY(resolution,bucket,outcome) REFERENCES rollups(resolution,bucket,outcome) ON DELETE CASCADE
) WITHOUT ROWID;
PRAGMA user_version=4;
