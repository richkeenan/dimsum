package admin_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinListHTTPRevisionAndMembership(t *testing.T) {
	service, store, path := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	w := request(h, "PATCH", "/api/v1/client-policy", mutation(t, store, map[string]any{"scope": "network", "subscribe": []any{map[string]any{"id": "work", "url": "builtin://work-compatibility", "dialect": "dns-adblock", "domain_kind": "suffix", "enabled": true}}, "fields": []any{map[string]any{"path": []string{"lists", "work"}, "value": true}}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	read := func() map[string]any {
		t.Helper()
		w := request(h, "GET", "/api/v1/builtin-lists/work", "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var v map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		return v
	}
	original := read()
	assert.Equal(t, false, original["customized"])
	baseline := original["entries"].([]any)[0].(map[string]any)["domain"].(string)
	mutate := func(action, domain string) {
		t.Helper()
		body := mutation(t, store, map[string]any{"action": action, "domain": domain})
		w := request(h, "PATCH", "/api/v1/builtin-lists/work", body)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	match := func(domain string) policy.Result {
		n, e := policy.NormalizeName(domain)
		require.NoError(t, e)
		return store.Snapshot().Policy().Match(n).Result
	}
	mutate("remove", baseline)
	assert.Equal(t, policy.Forward, match(baseline), "removal is not a deny rule")
	assert.Equal(t, true, read()["customized"])
	b, e := json.Marshal(map[string]any{"revision": original["revision"], "action": "reset"})
	require.NoError(t, e)
	assert.Equal(t, 409, request(h, "PATCH", "/api/v1/builtin-lists/work", string(b)).Code)
	mutate("restore", baseline)
	assert.Equal(t, policy.Allow, match(baseline))
	mutate("add", "Extra.Example.")
	assert.Equal(t, policy.Allow, match("child.extra.example"))
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	source = []byte(strings.Replace(string(source), "- extra.example", "- extra.example # owner exception", 1))
	require.NoError(t, os.WriteFile(path, source, 0600))
	mutate("remove", "extra.example")
	assert.Equal(t, policy.Forward, match("extra.example"))
	mutate("add", "extra.example")
	mutate("reset", "")
	assert.Equal(t, false, read()["customized"])
	assert.Equal(t, policy.Forward, match("extra.example"))
	assert.Equal(t, policy.Allow, match(baseline))
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(saved), "# owner exception")
	assert.True(t, strings.HasPrefix(string(saved), "# Isolated test input"))
	assert.Equal(t, 404, request(h, "GET", "/api/v1/builtin-lists/missing", "").Code)
	assert.Equal(t, 422, request(h, "PATCH", "/api/v1/builtin-lists/work", mutation(t, store, map[string]any{"action": "add", "domain": "||bad.example^"})).Code)
	w = request(h, "POST", "/api/v1/lists", mutation(t, store, map[string]any{"item": map[string]any{"id": "remote", "url": "https://example.test/list", "dialect": "domains", "domain_kind": "suffix", "enabled": false}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 404, request(h, "GET", "/api/v1/builtin-lists/remote", "").Code)
	assert.Equal(t, 404, request(h, "PATCH", "/api/v1/builtin-lists/remote", mutation(t, store, map[string]any{"action": "reset"})).Code)
}
