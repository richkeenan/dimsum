package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV2SourceMigrationPreservesUnknownIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.sqlite")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(initialSchema + bootCoverageSchema)
	require.NoError(t, err)
	_, err = raw.Exec("INSERT INTO rule_versions VALUES('old',1,2,'source: not-an-identity')")
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	db, err := Open(path)
	require.NoError(t, err)
	defer db.Close()
	var version int
	require.NoError(t, db.read.QueryRow("PRAGMA user_version").Scan(&version))
	assert.Equal(t, 4, version)
	var source, description string
	require.NoError(t, db.read.QueryRow("SELECT source_id,description FROM rule_versions").Scan(&source, &description))
	assert.Empty(t, source)
	assert.Equal(t, "source: not-an-identity", description)
	err = db.WriteBatch(t.Context(), "old", nil, BatchOptions{Rules: []RuleVersion{{Generation: 1, RuleID: 2, Description: description, SourceID: "new"}}})
	assert.ErrorContains(t, err, "immutable rule version conflict")
}
