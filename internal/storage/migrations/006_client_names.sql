CREATE TABLE client_names (
    kind TEXT NOT NULL CHECK (kind IN ('multicast', 'resolved')),
    address TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (length(scope) = 64),
    payload BLOB NOT NULL CHECK (length(payload) <= 65536),
    PRIMARY KEY (kind, address)
) WITHOUT ROWID;
PRAGMA user_version=6;
