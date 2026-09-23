package mcpserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionPathErrorAndRecoveryThroughMCP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	source, err := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, err)
	source = append(source, []byte("lists:\n  - id: example\n    url: https://example.test/hosts\n    dialect: hosts\n    domain_kind: exact\n    enabled: true\n")...)
	require.NoError(t, os.WriteFile(path, source, 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	service := control.New(control.Options{Store: store, ConfigPath: path})
	session := connect(t, admin.New(service, admin.Options{}).LocalHandler())
	revision := store.Inspect().SavedRevision
	result := call(t, session, "update_filter_lists", map[string]any{"body": map[string]any{
		"revision": revision, "edits": []any{map[string]any{"path": []string{"lists", "0", "enabled"}, "value": false}},
	}})
	require.True(t, result.IsError)
	b, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var response struct {
		Error struct {
			Code   string `json:"code"`
			Fields []struct {
				Path    []string `json:"path"`
				Message string   `json:"message"`
			} `json:"field_errors"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &response))
	assert.Equal(t, "bad_request", response.Error.Code)
	require.Len(t, response.Error.Fields, 1)
	assert.Equal(t, []string{"edits", "0", "path"}, response.Error.Fields[0].Path)
	assert.Contains(t, response.Error.Fields[0].Message, `["0","enabled"]`)
	assert.Equal(t, revision, store.Inspect().SavedRevision)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, source, after)
	result = call(t, session, "update_filter_lists", map[string]any{"body": map[string]any{
		"revision": revision, "edits": []any{map[string]any{"path": []string{"0", "enabled"}, "value": false}},
	}})
	require.False(t, result.IsError, "%v", result.StructuredContent)
	assert.False(t, store.Snapshot().Config().Lists[0].Enabled)
	for _, name := range []string{"r1---edge.example", "", ".", `\000\255.example`} {
		result = call(t, session, "explain_domain", map[string]any{"body": map[string]any{"name": name}})
		assert.False(t, result.IsError, "%v", result.StructuredContent)
	}
	result = call(t, session, "explain_domain", map[string]any{"body": map[string]any{"name": `bad\256.example`}})
	require.True(t, result.IsError)
	b, err = json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &response))
	assert.Equal(t, "bad_request", response.Error.Code)
	require.Len(t, response.Error.Fields, 1)
	assert.Equal(t, []string{"name"}, response.Error.Fields[0].Path)
}
