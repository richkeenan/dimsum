package mcpserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientPolicyMCPRealResponseSchemas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	source, err := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, source, 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	service := control.New(control.Options{Store: store, ConfigPath: path})
	session := connect(t, admin.New(service, admin.Options{}).LocalHandler())
	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	schemas := map[string]*jsonschema.Resolved{}
	for _, tool := range tools.Tools {
		if tool.OutputSchema != nil {
			encoded, err := json.Marshal(tool.OutputSchema)
			require.NoError(t, err)
			var schema jsonschema.Schema
			require.NoError(t, json.Unmarshal(encoded, &schema))
			schemas[tool.Name], err = schema.Resolve(nil)
			require.NoError(t, err)
		}
	}
	run := func(name string, args any) map[string]any {
		t.Helper()
		result := call(t, session, name, args)
		require.False(t, result.IsError, "%v", result.Content)
		b, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		var value map[string]any
		require.NoError(t, json.Unmarshal(b, &value))
		require.NotNil(t, schemas[name])
		require.NoError(t, schemas[name].Validate(value), "%s: %s", name, b)
		return value
	}
	body := map[string]any{"revision": store.Inspect().SavedRevision, "scope": "client", "id": "phone", "create": true, "name": "Phone", "selectors": map[string]any{"addresses": []string{"192.0.2.10"}}, "fields": []any{map[string]any{"path": []string{"blocking"}, "value": false}}}
	run("preview_client_policy", map[string]any{"body": body})
	run("update_client_policy", map[string]any{"body": body})
	read := run("get_client_policy", map[string]any{"scope": "client", "id": "phone"})
	assert.Equal(t, false, read["effective"].(map[string]any)["filtering"])
	run("get_client_policy", map[string]any{})
	inventory := run("list_clients", map[string]any{})
	summaries, ok := inventory["policy_summaries"].(map[string]any)
	require.True(t, ok)
	phone := summaries["phone"].(map[string]any)
	assert.Equal(t, false, phone["active"].(map[string]any)["filtering"])
	assert.NotContains(t, phone["desired"].(map[string]any), "rules")
	run("list_profiles", map[string]any{})
	run("explain_domain", map[string]any{"body": map[string]any{"name": "adult.example", "client_id": "phone"}})
	result := call(t, session, "update_client_policy", map[string]any{"body": body})
	assert.True(t, result.IsError)
}
