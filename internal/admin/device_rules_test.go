package admin_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeviceRulesLiveEditingAndReset(t *testing.T) {
	service, store, path := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	read := func() map[string]any {
		t.Helper()
		w := request(h, "GET", "/api/v1/device-rules", "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var got map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		return got
	}
	initial := read()
	w := request(h, "POST", "/api/v1/clients", mutation(t, store, map[string]any{"item": map[string]any{"address": "192.0.2.30", "name": "Owner device", "icon": "bell"}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, false, initial["customized"])
	base := initial["entries"].([]any)[0].(map[string]any)["rule"].(map[string]any)
	id := base["id"].(string)
	originalDomain := base["domains"].([]any)[0].(string)
	mutate := func(body map[string]any) {
		t.Helper()
		w := request(h, "PATCH", "/api/v1/device-rules", mutation(t, store, body))
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	names := clients.New(func() *clients.View { return store.Snapshot().Names() })
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	guess := func(domain string) clients.Name {
		names.ReplaceDNSActivity([]clients.DNSActivity{{Address: ip, Domain: domain, First: now.Add(-time.Minute), Last: now, Count: 2}})
		return names.Get(ip)
	}
	base["icon"] = "bell"
	base["domains"] = []string{"owner.example"}
	mutate(map[string]any{"action": "save", "id": id, "rule": base})
	assert.Equal(t, true, read()["customized"])
	assert.NotEqual(t, base["name"], guess(originalDomain).Name, "removed exact rules may still match the independent generic AWS IoT fallback")
	assert.Equal(t, "bell", guess("owner.example").Device.Icon)
	changes := store.Snapshot().Config().Naming.DNSGuesses
	assert.Equal(t, []string{"owner.example"}, changes.Rules[id].Additions)
	assert.Contains(t, changes.Rules[id].Exclusions, originalDomain)
	assert.Nil(t, changes.Rules[id].Name, "unchanged metadata must follow future baselines")
	mutate(map[string]any{"action": "enable", "id": id, "enabled": false})
	assert.Empty(t, names.Get(ip).Name, "retired activity must disappear immediately")
	mutate(map[string]any{"action": "reset", "id": id})
	assert.NotEmpty(t, guess(originalDomain).Name)
	assert.Empty(t, guess("owner.example").Name)
	mutate(map[string]any{"action": "save", "id": "custom", "rule": map[string]any{"id": "custom", "name": "Example appliance", "icon": "washing-machine", "domains": []string{"appliance.example"}}})
	assert.Equal(t, "Example appliance", guess("appliance.example").Name)
	mutate(map[string]any{"action": "save", "id": "custom", "rule": map[string]any{"id": "custom", "name": "Edited appliance", "domains": []string{"appliance.example"}}})
	assert.Equal(t, "Edited appliance", guess("appliance.example").Name)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	bad := mutation(t, store, map[string]any{"action": "save", "id": "custom", "rule": map[string]any{"id": "custom", "name": "Bad", "domains": []string{"*.example"}}})
	assert.Equal(t, 422, request(h, "PATCH", "/api/v1/device-rules", bad).Code)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	stale, err := json.Marshal(map[string]any{"revision": initial["revision"], "action": "reset"})
	require.NoError(t, err)
	assert.Equal(t, 409, request(h, "PATCH", "/api/v1/device-rules", string(stale)).Code)
	mutate(map[string]any{"action": "delete", "id": "custom"})
	assert.Empty(t, guess("appliance.example").Name)
	mutate(map[string]any{"action": "enable", "id": id, "enabled": false})
	mutate(map[string]any{"action": "reset"})
	assert.Equal(t, false, read()["customized"])
	assert.NotEmpty(t, guess(originalDomain).Name)
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(after), "# Isolated test input")
	assert.Equal(t, "Owner device", store.Snapshot().Config().Clients[0].Name)
	assert.Equal(t, "bell", store.Snapshot().Config().Clients[0].Icon)
}
