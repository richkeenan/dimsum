package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedHTTPAgentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	start := func() *app.Service {
		store, err := config.OpenStore(t.Context(), path, path+".state", config.StoreOptions{Offline: true})
		require.NoError(t, err)
		s := new(app.Service)
		require.NoError(t, s.StartManaged(t.Context(), store))
		t.Cleanup(func() { assert.NoError(t, s.Close()) })
		return s
	}
	s := start()
	base := "http://" + s.Addresses().Admin
	client := &http.Client{Timeout: 5 * time.Second}
	var cookie *http.Cookie
	var csrf string
	request := func(method, route, bearer string, body any, browser bool) (int, map[string]any) {
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			require.NoError(t, err)
		}
		r, err := http.NewRequest(method, base+route, bytes.NewReader(encoded))
		require.NoError(t, err)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if browser {
			r.AddCookie(cookie)
			r.Header.Set("Origin", base)
			r.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := client.Do(r)
		require.NoError(t, err)
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		var result map[string]any
		require.NoError(t, json.Unmarshal(b, &result), string(b))
		return res.StatusCode, result
	}
	login, err := client.Post(base+"/session", "application/json", bytes.NewBufferString(`{"password":"admin"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, login.StatusCode)
	require.NotEmpty(t, login.Cookies())
	cookie = login.Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	require.NoError(t, json.NewDecoder(login.Body).Decode(&session))
	login.Body.Close()
	csrf = session.CSRF
	status, created := request("POST", "/api/v1/tokens", "", map[string]any{"name": "My agent"}, true)
	require.Equal(t, http.StatusOK, status, created)
	token, ok := created["token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, token)
	id, ok := created["id"].(string)
	require.True(t, ok)
	status, spec := request("GET", "/api/v1/openapi.json", token, nil, false)
	require.Equal(t, http.StatusOK, status, spec)
	assert.Equal(t, "3.1.0", spec["openapi"])
	initialize := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "integration-test", "version": "1"}}}
	status, _ = request("POST", "/mcp", "", initialize, true)
	assert.Equal(t, http.StatusUnauthorized, status, "browser cookie must not grant MCP access")
	status, initialized := request("POST", "/mcp", token, initialize, false)
	require.Equal(t, http.StatusOK, status, initialized)
	assert.Contains(t, initialized, "result")
	status, tools := request("POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, false)
	require.Equal(t, http.StatusOK, status, tools)
	encodedTools, err := json.Marshal(tools)
	require.NoError(t, err)
	assert.Contains(t, string(encodedTools), "get_summary")
	assert.Contains(t, string(encodedTools), "inputSchema")
	status, summary := request("POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "get_summary", "arguments": map[string]any{}}}, false)
	require.Equal(t, http.StatusOK, status, summary)
	result, ok := summary["result"].(map[string]any)
	require.True(t, ok, summary)
	assert.NotEqual(t, true, result["isError"], summary)
	assert.Contains(t, result, "structuredContent")
	status, settings := request("GET", "/api/v1/settings", token, nil, false)
	require.Equal(t, http.StatusOK, status, settings)
	activation, ok := settings["status"].(map[string]any)
	require.True(t, ok, settings)
	mutation := map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{
		"name": "update_settings", "arguments": map[string]any{"body": map[string]any{
			"revision": activation["saved_revision"],
			"edits":    []any{map[string]any{"path": []string{"cache", "max_stale_seconds"}, "value": 120}},
		}},
	}}
	status, saved := request("POST", "/mcp", token, mutation, false)
	require.Equal(t, http.StatusOK, status, saved)
	savedResult, ok := saved["result"].(map[string]any)
	require.True(t, ok, saved)
	require.NotEqual(t, true, savedResult["isError"], saved)
	status, stale := request("POST", "/mcp", token, mutation, false)
	require.Equal(t, http.StatusOK, status, stale)
	staleResult, ok := stale["result"].(map[string]any)
	require.True(t, ok, stale)
	assert.Equal(t, true, staleResult["isError"], "stale revisions must remain conflicts through MCP")
	status, updated := request("GET", "/api/v1/settings", token, nil, false)
	require.Equal(t, http.StatusOK, status, updated)
	configuration, ok := updated["config"].(map[string]any)
	require.True(t, ok, updated)
	cache, ok := configuration["cache"].(map[string]any)
	require.True(t, ok, configuration)
	assert.Equal(t, float64(120), cache["max_stale_seconds"])

	require.NoError(t, s.Close())
	s = start()
	base = "http://" + s.Addresses().Admin
	status, listed := request("GET", "/api/v1/tokens", token, nil, false)
	require.Equal(t, http.StatusOK, status, listed)
	encodedList, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedList), token)
	assert.Contains(t, string(encodedList), "My agent")
	status, revoked := request("DELETE", "/api/v1/tokens/"+id, token, nil, false)
	require.Equal(t, http.StatusOK, status, revoked)
	status, _ = request("POST", "/mcp", token, initialize, false)
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = request("GET", "/api/v1/settings", token, nil, false)
	assert.Equal(t, http.StatusUnauthorized, status)
}
