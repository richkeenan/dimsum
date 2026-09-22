package admin_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dashboardFixture(t *testing.T) (*control.Service, *config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	b, err := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, err)
	b = []byte(strings.Replace(string(b), "listen: \"127.0.0.1:0\"", "listen: \"127.0.0.1:8080\"", 1))
	require.NoError(t, os.WriteFile(path, b, 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	return control.New(control.Options{Store: store, ConfigPath: path}), store, path
}

func TestLocalRecordDashboardHostApproval(t *testing.T) {
	service, store, path := dashboardFixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	w := request(h, "POST", "/api/v1/records", mutation(t, store, map[string]any{"item": map[string]any{"name": "dashboard.test", "type": "A", "value": "127.0.0.1", "ttl": 300}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		DashboardHosts []struct{ Name, Host, URL string } `json:"dashboard_hosts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result.DashboardHosts, 1)
	assert.Equal(t, "dashboard.test:8080", result.DashboardHosts[0].Host)
	assert.Equal(t, "http://dashboard.test:8080/", result.DashboardHosts[0].URL)
	assert.Empty(t, store.Snapshot().Config().Admin.AllowedHosts)
	approval := mutation(t, store, map[string]any{"accept_admin_host": "dashboard.test"})
	w = request(h, "PATCH", "/api/v1/records", approval)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"dashboard.test:8080"}, store.Snapshot().Config().Admin.AllowedHosts)
	assert.False(t, store.Inspect().RestartRequired)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	d, err := config.Parse(b)
	require.NoError(t, err)
	assert.Equal(t, []string{"dashboard.test:8080"}, d.Config().Admin.AllowedHosts)
	assert.True(t, strings.HasPrefix(string(b), "# Isolated test input"))
	assert.Equal(t, 409, request(h, "PATCH", "/api/v1/records", approval).Code)
	w = request(h, "PATCH", "/api/v1/records", mutation(t, store, map[string]any{"accept_admin_host": "attacker.test"}))
	assert.Equal(t, 422, w.Code)
}

func TestHostApprovalIsLiveAndRevocable(t *testing.T) {
	service, store, _ := dashboardFixture(t)
	hash, err := admin.HashPassword("a-long-test-password")
	require.NoError(t, err)
	server := admin.New(service, admin.Options{PasswordHash: hash})
	h := server.Handler()
	check := func() int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://dashboard.test:8080/health/live", nil))
		return w.Code
	}
	assert.Equal(t, 403, check())
	local := server.LocalHandler()
	w := request(local, "POST", "/api/v1/records", mutation(t, store, map[string]any{"item": map[string]any{"name": "dashboard.test", "type": "A", "value": "127.0.0.1", "ttl": 300}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	w = request(local, "PATCH", "/api/v1/records", mutation(t, store, map[string]any{"accept_admin_host": "dashboard.test"}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 200, check())
	for _, tc := range []struct {
		origin string
		status int
	}{{"http://dashboard.test:8080", 200}, {"http://attacker.test", 403}} {
		r := httptest.NewRequest("POST", "http://dashboard.test:8080/session", strings.NewReader(`{"password":"a-long-test-password"}`))
		r.Header.Set("Origin", tc.origin)
		login := httptest.NewRecorder()
		h.ServeHTTP(login, r)
		assert.Equal(t, tc.status, login.Code, login.Body.String())
	}
	w = request(local, "PATCH", "/api/v1/settings", mutation(t, store, map[string]any{"edits": []any{map[string]any{"path": []string{"admin", "allowed_hosts", "0"}, "value": "other.test:8080"}}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 403, check())
}

func TestHostApprovalPreservesExistingFlowList(t *testing.T) {
	service, store, path := dashboardFixture(t)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	b = []byte(strings.Replace(string(b), "admin:\n", "admin:\n  allowed_hosts: [\"existing.test:8080\"] # Keep this comment\n", 1))
	require.NoError(t, os.WriteFile(path, b, 0600))
	_, err = store.Reload(t.Context())
	require.NoError(t, err)
	h := admin.New(service, admin.Options{}).LocalHandler()
	w := request(h, "POST", "/api/v1/records", mutation(t, store, map[string]any{"item": map[string]any{"name": "dashboard.test", "type": "A", "value": "127.0.0.1", "ttl": 300}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	w = request(h, "PATCH", "/api/v1/records", mutation(t, store, map[string]any{"accept_admin_host": "dashboard.test"}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.ElementsMatch(t, []string{"existing.test:8080", "dashboard.test:8080"}, store.Snapshot().Config().Admin.AllowedHosts)
	b, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"existing.test:8080"] # Keep this comment`)
}

func TestAcceptedDefaultPortMatchesBrowserHost(t *testing.T) {
	service, store, path := dashboardFixture(t)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	b = []byte(strings.Replace(string(b), "admin:\n", "admin:\n  allowed_hosts: [\"dashboard.test:80\"]\n", 1))
	require.NoError(t, os.WriteFile(path, b, 0600))
	_, err = store.Reload(t.Context())
	require.NoError(t, err)
	h := admin.New(service, admin.Options{}).Handler()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://dashboard.test/health/live", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	h.ServeHTTP(w, r)
	assert.Equal(t, 200, w.Code, w.Body.String())
}
