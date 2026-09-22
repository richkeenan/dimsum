package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (*control.Service, *config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	b, e := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, e)
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, b, 0600))
	store, e := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, e)
	return control.New(control.Options{Store: store, ConfigPath: path}), store, path
}
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://admin.test"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func mutation(t *testing.T, store *config.Store, extra map[string]any) string {
	t.Helper()
	extra["revision"] = store.Inspect().SavedRevision
	b, e := json.Marshal(extra)
	require.NoError(t, e)
	return string(b)
}

func TestMutationsConflictInvalidActivationAndRuleExplanation(t *testing.T) {
	service, store, path := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	appendRule := mutation(t, store, map[string]any{"item": map[string]any{"id": "agent-rule", "kind": "exact", "action": "deny", "pattern": "ads.example", "enabled": true}})
	w := request(h, "POST", "/api/v1/rules", appendRule)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"active_generation":"2"`)
	w = request(h, "POST", "/api/v1/rules/test", `{"name":"ADS.EXAMPLE.","qtype":"A","generation":"2"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "custom:agent-rule")
	assert.Contains(t, w.Body.String(), `"normalized":"ads.example"`)
	w = request(h, "POST", "/api/v1/rules", appendRule)
	assert.Equal(t, 409, w.Code)
	before, e := os.ReadFile(path)
	require.NoError(t, e)
	w = request(h, "PATCH", "/api/v1/rules", mutation(t, store, map[string]any{"edits": []any{map[string]any{"path": []string{"0", "kind"}, "value": "invalid"}}}))
	assert.Equal(t, 422, w.Code)
	after, e := os.ReadFile(path)
	require.NoError(t, e)
	assert.Equal(t, before, after)
	assert.Equal(t, uint64(2), store.Snapshot().Generation())
	// Even invalid external text changes the revision and must never be overwritten.
	require.NoError(t, os.WriteFile(path, append(after, []byte("unknown_setting: true\n")...), 0600))
	w = request(h, "POST", "/api/v1/rules", appendRule)
	assert.Equal(t, 409, w.Code)
	w = request(h, "GET", "/api/v1/settings", "")
	assert.Contains(t, w.Body.String(), "configuration_error")
}

func TestStagingPauseAndResumePreserveText(t *testing.T) {
	service, store, path := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	body := mutation(t, store, map[string]any{"edits": []any{map[string]any{"path": []string{"cache", "max_stale_seconds"}, "value": 120}}})
	w := request(h, "POST", "/api/v1/config/transactions", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	var stage map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stage))
	assert.Equal(t, uint64(1), store.Snapshot().Generation())
	w = request(h, "POST", "/api/v1/config/transactions/"+stage["id"]+"/commit", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 120, store.Snapshot().Config().Cache.MaxStaleSeconds)
	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	w = request(h, "PUT", "/api/v1/blocking", mutation(t, store, map[string]any{"enabled": false, "pause_until": until}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.True(t, store.Snapshot().Filtering().Paused(time.Now()))
	w = request(h, "PUT", "/api/v1/blocking", mutation(t, store, map[string]any{"enabled": true}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.False(t, store.Snapshot().Filtering().Paused(time.Now()))
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	assert.True(t, bytes.HasPrefix(b, []byte("# Isolated test input")))
}

func TestUnavailableIsNotEmptyHistory(t *testing.T) {
	service, _, _ := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	for _, resource := range []string{"summary", "timeseries", "queries", "queries/1", "rankings"} {
		w := request(h, "GET", "/api/v1/"+resource, "")
		assert.Equal(t, 503, w.Code, resource)
		assert.Contains(t, w.Body.String(), `"code":"unavailable"`)
	}
	w := request(h, "POST", "/api/v1/jobs", `{"kind":"backup"}`)
	assert.Equal(t, 503, w.Code)
}

func TestRecordsNumericValuesAndInitiallyMissingUpstreams(t *testing.T) {
	service, store, _ := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	w := request(h, "POST", "/api/v1/records", mutation(t, store, map[string]any{"item": map[string]any{"name": "printer.home.arpa", "type": "A", "value": "192.0.2.5", "ttl": 60}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, uint32(60), store.Snapshot().Config().Records[0].TTL)
	w = request(h, "POST", "/api/v1/upstreams", mutation(t, store, map[string]any{"item": "192.0.2.53:53"}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"192.0.2.53:53"}, store.Snapshot().Config().DNS.Upstreams)
}

func TestUnixPermissionsAndCLIParity(t *testing.T) {
	service, store, _ := fixture(t)
	server := admin.New(service, admin.Options{})
	dir, e := os.MkdirTemp("", "ds-")
	require.NoError(t, e)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0700))
	socket := filepath.Join(dir, "control.sock")
	listener, e := server.ListenUnix(socket)
	require.NoError(t, e)
	info, e := os.Stat(socket)
	require.NoError(t, e)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	httpServer := &http.Server{Handler: server.LocalHandler()}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	t.Cleanup(func() { httpServer.Close(); e := <-done; assert.ErrorIs(t, e, http.ErrServerClosed) })
	var out, stderr bytes.Buffer
	code := cli.Run(t.Context(), []string{"--socket", socket, "settings"}, &out, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.JSONEq(t, request(server.LocalHandler(), "GET", "/api/v1/settings", "").Body.String(), out.String())
	out.Reset()
	body := mutation(t, store, map[string]any{"item": map[string]any{"address": "192.0.2.4", "name": "workstation"}})
	require.Equal(t, 0, cli.Run(t.Context(), []string{"--socket", socket, "add", "clients", body}, &out, &stderr), stderr.String())
	assert.Equal(t, "workstation", store.Snapshot().Config().Clients[0].Name)
	out.Reset()
	require.Equal(t, 6, cli.Run(t.Context(), []string{"--socket", socket, "rankings"}, &out, &stderr))
	assert.Empty(t, out.String())
	assert.Contains(t, stderr.String(), "unavailable")
	_, e = server.ListenUnix(socket)
	assert.Error(t, e)
	unsafe := t.TempDir()
	require.NoError(t, os.Chmod(unsafe, 0755))
	_, e = server.ListenUnix(filepath.Join(unsafe, "socket"))
	assert.Error(t, e)
}

func TestSSEReconnectAndConnectionCap(t *testing.T) {
	service, _, _ := fixture(t)
	server := httptest.NewServer(admin.New(service, admin.Options{}).LocalHandler())
	defer server.Close()
	var bodies []io.ReadCloser
	defer func() {
		for _, b := range bodies {
			b.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		r, e := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/api/v1/events", nil)
		require.NoError(t, e)
		r.Header.Set("Last-Event-ID", "prior-process-id")
		resp, e := server.Client().Do(r)
		require.NoError(t, e)
		bodies = append(bodies, resp.Body)
		require.Equal(t, 200, resp.StatusCode)
		b := make([]byte, len("event: reset\n"))
		_, e = io.ReadFull(resp.Body, b)
		require.NoError(t, e)
		assert.Equal(t, "event: reset\n", string(b))
	}
	resp, e := server.Client().Get(server.URL + "/api/v1/events")
	require.NoError(t, e)
	defer resp.Body.Close()
	assert.Equal(t, 429, resp.StatusCode)
}

func TestBoundedJobsAndFailure(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	service := control.New(control.Options{Jobs: map[string]func(context.Context, json.RawMessage) (any, error){"backup": func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		select {
		case <-release:
			return nil, io.ErrUnexpectedEOF
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}})
	job, e := service.StartJob(t.Context(), "backup", nil)
	require.NoError(t, e)
	assert.Equal(t, "1", job.ID)
	<-started
	_, e = service.StartJob(t.Context(), "backup", nil)
	assert.ErrorIs(t, e, control.ErrBusy)
	close(release)
	require.Eventually(t, func() bool { return service.Jobs()[0].State == "failed" }, time.Second, time.Millisecond)
	assert.Contains(t, service.Jobs()[0].Error, "unexpected EOF")
}
