package admin_test

import (
	"encoding/json"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientPolicyHTTPTransactionAndReset(t *testing.T) {
	service, store, _ := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	body := mutation(t, store, map[string]any{"scope": "client", "id": "phone", "create": true, "name": "Phone", "selectors": map[string]any{"addresses": []string{"192.0.2.10"}}, "subscribe": []any{map[string]any{"id": "adult", "url": "https://example.test/adult", "dialect": "domains", "domain_kind": "suffix", "enabled": true}}, "fields": []any{map[string]any{"path": []string{"lists", "adult"}, "value": true}}})
	w := request(h, "POST", "/api/v1/client-policy/preview", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"changed_clients":["phone"]`)
	w = request(h, "PATCH", "/api/v1/client-policy", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "suffix", string(store.Snapshot().Config().Lists[0].DomainKind))
	w = request(h, "GET", "/api/v1/clients", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"policy_id":"phone"`)
	w = request(h, "GET", "/api/v1/client-policy?scope=client&id=phone", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"value":true,"source":{"kind":"client","id":"phone"}`)
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.NotNil(t, response["active"])
	w = request(h, "PATCH", "/api/v1/client-policy", mutation(t, store, map[string]any{"scope": "client", "id": "phone", "fields": []any{map[string]any{"path": []string{"lists", "adult"}, "reset": true}}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Empty(t, store.Snapshot().Config().Clients[0].Overrides.Lists)
	w = request(h, "POST", "/api/v1/rules/test", `{"name":"adult.example","client_id":"phone"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"client_id":"phone"`)
}
