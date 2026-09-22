package admin_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDHCPLeaseMalformedRawQueryHTTPAndCLI(t *testing.T) {
	_, store, path := fixture(t)
	var inspections atomic.Int32
	service := control.New(control.Options{Store: store, ConfigPath: path, DHCPInspect: func() dhcp.LeaseSnapshot {
		inspections.Add(1)
		return dhcp.LeaseSnapshot{Generation: 1, Revision: 1}
	}})
	server := admin.New(service, admin.Options{})
	h := server.LocalHandler()
	dir, err := os.MkdirTemp("", "dhcp-query-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := server.ListenUnix(socket)
	require.NoError(t, err)
	srv := &http.Server{Handler: h}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() { srv.Close(); assert.ErrorIs(t, <-done, http.ErrServerClosed) })

	for _, query := range []string{
		"cursor=%ZZ", "state=bound%ZZ", "limit=1;state=bound",
		"state=bound&state=%ZZ", "limit=1&cursor=%ZZ",
		"limit=1&state=bound;hostname=printer", "%ZZ=bound", "cursor=%",
	} {
		t.Run(query, func(t *testing.T) {
			before := inspections.Load()
			w := request(h, "GET", "/api/v1/dhcp/leases?"+query, "")
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"bad_request"`)
			assert.NotContains(t, w.Body.String(), `"items"`)

			var out, stderr bytes.Buffer
			code := cli.Run(t.Context(), []string{"--socket", socket, "dhcp-leases", "--query", query}, &out, &stderr)
			assert.Equal(t, 4, code, stderr.String())
			assert.Empty(t, out.String(), "a malformed query must not return a successful lease page")
			assert.Contains(t, stderr.String(), `"code":"bad_request"`)
			assert.Equal(t, before, inspections.Load(), "reject malformed raw input before reading leases")
		})
	}

	// Well-formed escaping still reaches the shared filter operation.
	query := "limit=1&state=%62ound"
	w := request(h, "GET", "/api/v1/dhcp/leases?"+query, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out, stderr bytes.Buffer
	require.Equal(t, 0, cli.Run(t.Context(), []string{"--socket", socket, "dhcp-leases", "--query", query}, &out, &stderr), stderr.String())
	assert.JSONEq(t, w.Body.String(), out.String())
	assert.EqualValues(t, 2, inspections.Load())
}

func TestDHCPHTTPAndCLIRevisionCRUD(t *testing.T) {
	service, store, path := fixture(t)
	server := admin.New(service, admin.Options{})
	h := server.LocalHandler()
	dir, e := os.MkdirTemp("", "dhcp-cli-")
	require.NoError(t, e)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, e := server.ListenUnix(socket)
	require.NoError(t, e)
	srv := &http.Server{Handler: h}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() { srv.Close(); assert.ErrorIs(t, <-done, http.ErrServerClosed) })
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, stderr bytes.Buffer
		code := cli.Run(t.Context(), append([]string{"--socket", socket}, args...), &out, &stderr)
		return code, out.String() + stderr.String()
	}
	for _, name := range []string{"dhcp", "dhcp-status", "dhcp-leases", "dhcp-reservations"} {
		code, text := run("get", name)
		assert.Equal(t, 0, code, text)
		assert.True(t, json.Valid([]byte(text)))
		if name == "dhcp" {
			var result struct {
				Config dhcp.Settings `json:"config"`
				Setup  dhcp.Setup    `json:"setup"`
			}
			require.NoError(t, json.Unmarshal([]byte(text), &result))
			assert.False(t, result.Config.Enabled)
			assert.Empty(t, result.Config.Interface, "inspection must not save detected network values")
			assert.Equal(t, 0, result.Config.LeaseSeconds)
			assert.Equal(t, 86400, result.Setup.Config.LeaseSeconds)
			assert.Equal(t, "home.arpa", result.Setup.Config.LocalDomain)
			assert.False(t, result.Setup.Config.Enabled)
			assert.Equal(t, 0, store.Snapshot().Config().DHCP.LeaseSeconds)
		}
	}
	body := mutation(t, store, map[string]any{"edits": []config.Edit{{Path: []string{"subnet"}, Value: "192.0.2.0/24"}, {Path: []string{"max_leases"}, Value: 1}}})
	code, text := run("patch", "dhcp", body)
	require.Equal(t, 0, code, text)
	code, text = run("patch", "dhcp", body)
	assert.Equal(t, 5, code, text)
	assert.Contains(t, text, "revision_conflict")
	add := mutation(t, store, map[string]any{"item": map[string]any{"id": "printer", "mac": "02:00:00:00:00:10", "address": "192.0.2.20"}})
	w := request(h, "POST", "/api/v1/dhcp/reservations", add)
	require.Equal(t, 200, w.Code, w.Body.String())
	code, text = run("patch", "dhcp/reservations/printer", mutation(t, store, map[string]any{"edits": []config.Edit{{Path: []string{"hostname"}, Value: "lab-printer"}}}))
	require.Equal(t, 0, code, text)
	code, text = run("dhcp-reservations")
	require.Equal(t, 0, code, text)
	assert.Contains(t, text, "lab-printer")
	w = request(h, "POST", "/api/v1/dhcp/reservations", mutation(t, store, map[string]any{"item": map[string]any{"id": "other", "mac": "02:00:00:00:00:11", "address": "192.0.2.21"}}))
	assert.Equal(t, 429, w.Code)
	before, e := os.ReadFile(path)
	require.NoError(t, e)
	for _, invalid := range []string{`null`, `{"revision":"x","force_release":true}`, fmt.Sprintf(`{"revision":%q,"edits":[{"path":["enabled"],"value":true}],"index":0}`, store.Inspect().SavedRevision), fmt.Sprintf(`{"revision":%q,"edits":[{"path":["reservations"],"value":[]}]}`, store.Inspect().SavedRevision)} {
		w = request(h, "PATCH", "/api/v1/dhcp", invalid)
		assert.GreaterOrEqual(t, w.Code, 400, w.Body.String())
	}
	after, e := os.ReadFile(path)
	require.NoError(t, e)
	assert.Equal(t, before, after)
	assert.True(t, bytes.HasPrefix(after, []byte("# Isolated test input")))
	code, text = run("delete", "dhcp/reservations/printer", mutation(t, store, map[string]any{}))
	require.Equal(t, 0, code, text)
	w = request(h, "DELETE", "/api/v1/dhcp/reservations/printer", mutation(t, store, map[string]any{}))
	assert.Equal(t, 404, w.Code)
	assert.Empty(t, store.Snapshot().Config().DHCP.Reservations)
	code, text = run("add", "dhcp/reservations", mutation(t, store, map[string]any{"item": map[string]any{"id": "replacement", "client_id": "00ff", "address": "192.0.2.21"}}))
	require.Equal(t, 0, code, text, "an emptied collection remains editable")
	code, text = run("patch", "dhcp/reservations/replacement", mutation(t, store, map[string]any{"edits": []config.Edit{{Path: []string{"client_id"}, Value: ""}, {Path: []string{"mac"}, Value: "02:00:00:00:00:12"}}}))
	require.Equal(t, 0, code, text, "identity kind changes validate as one grouped candidate")
	w = request(h, "GET", "/api/v1/dhcp/leases?limit=256", "")
	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"runtime_available":false`)
	w = request(h, "GET", "/api/v1/dhcp/leases?limit=257", "")
	assert.Equal(t, 400, w.Code)
}

func TestDHCPRoutesInheritBearerHostOrigin(t *testing.T) {
	service, _, _ := fixture(t)
	tokens, e := admin.OpenTokenStore(filepath.Join(t.TempDir(), "secrets", "tokens.json"))
	require.NoError(t, e)
	credential, e := tokens.Create("fixture")
	require.NoError(t, e)
	h := admin.New(service, admin.Options{Tokens: tokens, AllowedHosts: []string{"admin.test"}}).Handler()
	for _, p := range []string{"dhcp", "dhcp/status", "dhcp/leases", "dhcp/reservations", "dhcp/reservations/printer"} {
		for _, method := range []string{"GET", "PATCH", "POST", "DELETE"} {
			for _, mode := range []string{"missing", "host", "origin", "revoked"} {
				r := httptest.NewRequest(method, "http://admin.test/api/v1/"+p, strings.NewReader(`{}`))
				if mode != "missing" {
					r.Header.Set("Authorization", "Bearer "+credential.Token)
				}
				want := 401
				if mode == "host" {
					r.Host = "evil.test"
					want = 403
				}
				if mode == "origin" {
					r.Header.Set("Origin", "http://evil.test")
					want = 403
				}
				if mode == "revoked" {
					r.Header.Set("Authorization", "Bearer invalid")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				assert.Equal(t, want, w.Code, p+" "+method+" "+mode)
			}
		}
	}
	r := httptest.NewRequest("GET", "http://admin.test/api/v1/dhcp", nil)
	r.Header.Set("Authorization", "Bearer "+credential.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, tokens.Revoke(credential.ID))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, 401, w.Code)
}
