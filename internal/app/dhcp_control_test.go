package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/mcpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDHCPDiagnosticPassiveReadsAndPreparationGate(t *testing.T) {
	sup := NewDHCPSupervisor(t.TempDir(), nil)
	service := &Service{dhcp: sup}
	var opens atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	sup.openLink = func(s dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		opens.Add(1)
		close(entered)
		<-release
		return fixtureDHCPLink(s)
	}
	_ = service.DHCPStatus()
	_ = service.DHCPInspect()
	assert.Zero(t, opens.Load())
	check := dhcpDiagnostic(service, func() (dhcp.Settings, uint64) { s := dhcpSettings(); s.Enabled = false; return s, 1 })
	for _, input := range []string{`null`, `{"unknown":true}`, `{"timeout_ms":99}`, `{"timeout_ms":3001}`} {
		_, err := check(t.Context(), json.RawMessage(input))
		assert.ErrorIs(t, err, control.BadRequest)
	}
	assert.Zero(t, opens.Load())
	done := make(chan error, 1)
	go func() { _, err := check(context.Background(), json.RawMessage(`{}`)); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, sup.Reconcile(ctx, dhcp.Settings{}, 2), context.DeadlineExceeded, "temporary diagnostic sockets exclude runtime preparation")
	close(release)
	require.NoError(t, <-done)
	assert.EqualValues(t, 1, opens.Load())
	assert.Equal(t, "disabled", sup.Status().State)
	require.NoError(t, sup.Reconcile(t.Context(), dhcp.Settings{}, 2))
}

func TestDHCPDiagnosticTimeoutRetainsPreparationOwnership(t *testing.T) {
	sup := NewDHCPSupervisor(t.TempDir(), nil)
	service := &Service{dhcp: sup}
	entered, release := make(chan struct{}), make(chan struct{})
	link := &appDHCPLink{done: make(chan struct{})}
	sup.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		close(entered)
		<-release
		return link, nil, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { _, err := service.dhcpDiagnosticReadiness(ctx, dhcpSettings()); finished <- err }()
	<-entered
	cancel()
	require.ErrorIs(t, <-finished, context.Canceled)
	assert.Len(t, sup.gate, 1, "timed-out caller does not release the preparation gate")
	assert.Equal(t, "disabled", service.DHCPStatus().State, "inspection stays available")
	close(release)
	require.Eventually(t, func() bool { return len(sup.gate) == 0 }, time.Second, time.Millisecond)
	select {
	case <-link.done:
	default:
		t.Fatal("temporary socket was not closed before releasing ownership")
	}
}

type controlFailWriter struct {
	dhcp.LeaseWriter
	failed atomic.Bool
}

func (w *controlFailWriter) Err() error {
	if w.failed.Load() {
		return dhcp.ErrStoreUncertain
	}
	return w.LeaseWriter.Err()
}

func TestDHCPSharedMCPRuntimeWorkflow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`# Operator comment survives agent edits.
version: 1
dns:
  listen: ["0.0.0.0:53"]
  upstreams: ["192.0.2.53:53"]
admin:
  listen: "127.0.0.1:0"
paths:
  data_dir: ./data
  secrets_dir: ./secrets
dhcp:
  enabled: false
  interface: fixture0
  server_ip: 192.0.2.2
  subnet: 192.0.2.0/24
  gateway: 192.0.2.1
  range_start: 192.0.2.100
  range_end: 192.0.2.110
  lease_seconds: 3600
  local_domain: home.arpa
`), 0600))
	store, e := config.OpenStore(t.Context(), path, filepath.Join(dir, "config-state"), config.StoreOptions{Offline: true})
	require.NoError(t, e)
	sup := NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
	sup.openLink = fixtureDHCPLink
	service := &Service{dhcp: sup}
	t.Cleanup(func() { assert.NoError(t, sup.Close(context.Background())) })
	require.NoError(t, sup.Reconcile(t.Context(), store.Snapshot().Config().DHCP, store.Snapshot().Generation()))
	_, e = os.Stat(filepath.Join(dir, "dhcp"))
	require.True(t, os.IsNotExist(e), "disabled reconciliation must not create storage")
	// Synthetic durable ownership recovered by the real supervisor/runtime.
	db, _, e := dhcp.OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
	require.NoError(t, e)
	mac := [6]byte{2, 0, 0, 0, 0, 1}
	expiry := time.Now().UTC().Add(time.Hour)
	lease := dhcp.Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: netip.MustParseAddr("192.0.2.100"), Hostname: "lab-client", State: dhcp.Bound, Expiry: expiry, HoldUntil: expiry}
	require.NoError(t, db.Submit(t.Context(), dhcp.Mutation{Kind: dhcp.PutLease, Token: dhcp.Token{Generation: 1, Sequence: 1}, Lease: lease}))
	committed, ok := <-db.Results()
	require.True(t, ok)
	require.NoError(t, committed.Err)
	require.NoError(t, db.Close(t.Context()))
	var writer *controlFailWriter
	sup.openStore = func(p string) (dhcp.LeaseWriter, dhcp.LeaseRecovery, error) {
		db, recovery, err := dhcp.OpenLeaseStore(p, 4096, nil)
		if err != nil {
			return nil, recovery, err
		}
		writer = &controlFailWriter{LeaseWriter: db}
		return writer, recovery, nil
	}
	// This fixture uses an in-memory packet transport, available on every test OS.
	shared := control.New(control.Options{Store: store, ConfigPath: path, BootID: "fixture-boot", DHCPAvailability: func() dhcp.Availability { return dhcp.Availability{Supported: true, Code: "supported"} }, DHCPStatus: func() any { return safeJSON(service.DHCPStatus()) }, DHCPInspect: service.DHCPInspect, Jobs: map[string]func(context.Context, json.RawMessage) (any, error){"dhcp-check": dhcpDiagnostic(service, func() (dhcp.Settings, uint64) { snap := store.Snapshot(); return snap.Config().DHCP, snap.Generation() })}})
	t.Cleanup(shared.Close)
	api := admin.New(shared, admin.Options{}).LocalHandler()
	handler, e := mcpserver.New(api)
	require.NoError(t, e)
	transport := httptest.NewServer(handler)
	t.Cleanup(transport.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "dhcp-test", Version: "1"}, nil)
	session, e := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: transport.URL, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, e)
	t.Cleanup(func() { _ = session.Close() })
	listing, e := session.ListTools(t.Context(), nil)
	require.NoError(t, e)
	tools := map[string]*mcp.Tool{}
	for _, tool := range listing.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"get_dhcp", "get_dhcp_status", "list_dhcp_leases", "list_dhcp_reservations", "update_dhcp", "add_dhcp_reservation", "update_dhcp_reservation", "remove_dhcp_reservation"} {
		require.Contains(t, tools, name)
		assert.Equal(t, strings.HasPrefix(name, "get_") || strings.HasPrefix(name, "list_"), tools[name].Annotations.ReadOnlyHint)
	}
	call := func(name string, args any, wantError bool) map[string]any {
		t.Helper()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		require.Equal(t, wantError, result.IsError, result)
		b, err := json.Marshal(tools[name].OutputSchema)
		require.NoError(t, err)
		var schema jsonschema.Schema
		require.NoError(t, json.Unmarshal(b, &schema))
		resolved, err := schema.Resolve(nil)
		require.NoError(t, err)
		if !wantError {
			require.NoError(t, resolved.Validate(result.StructuredContent))
		}
		value, ok := result.StructuredContent.(map[string]any)
		require.True(t, ok)
		return value
	}
	inspected := call("get_dhcp", map[string]any{}, false)
	revision := inspected["status"].(map[string]any)["saved_revision"]
	edit := map[string]any{"body": map[string]any{"revision": revision, "edits": []any{map[string]any{"path": []string{"enabled"}, "value": true}}}}
	saved := call("update_dhcp", edit, false)
	assert.Equal(t, "2", saved["active_generation"])
	status := call("get_dhcp_status", map[string]any{}, false)
	assert.Equal(t, "1", status["dhcp"].(map[string]any)["applied_generation"], "save does not promise DHCP application")
	require.NoError(t, sup.Reconcile(t.Context(), store.Snapshot().Config().DHCP, store.Snapshot().Generation()))
	status = call("get_dhcp_status", map[string]any{}, false)
	assert.Equal(t, "running", status["dhcp"].(map[string]any)["state"])
	assert.Equal(t, "2", status["dhcp"].(map[string]any)["applied_generation"])
	leases := call("list_dhcp_leases", map[string]any{"limit": 256, "state": "bound"}, false)
	require.Len(t, leases["items"], 1)
	invalidCursor := call("list_dhcp_leases", map[string]any{"cursor": "broken"}, true)
	assert.Equal(t, "bad_request", invalidCursor["error"].(map[string]any)["code"])
	badLimit, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_dhcp_leases", Arguments: map[string]any{"limit": 257}})
	require.NoError(t, err)
	assert.True(t, badLimit.IsError)
	conflict := call("update_dhcp", edit, true)
	assert.Equal(t, "revision_conflict", conflict["error"].(map[string]any)["code"])
	call("add_dhcp_reservation", map[string]any{"body": map[string]any{"revision": store.Inspect().SavedRevision, "item": map[string]any{"id": "printer", "mac": "02:00:00:00:00:20", "address": "192.0.2.20"}}}, false)
	require.NoError(t, sup.Reconcile(t.Context(), store.Snapshot().Config().DHCP, store.Snapshot().Generation()))
	call("update_dhcp_reservation", map[string]any{"id": "printer", "body": map[string]any{"revision": store.Inspect().SavedRevision, "edits": []any{map[string]any{"path": []string{"hostname"}, "value": "lab-printer"}}}}, false)
	require.NoError(t, sup.Reconcile(t.Context(), store.Snapshot().Config().DHCP, store.Snapshot().Generation()))
	call("list_dhcp_reservations", map[string]any{}, false)
	// Ownership conflict is surfaced by the application boundary, not hidden by save success.
	call("update_dhcp_reservation", map[string]any{"id": "printer", "body": map[string]any{"revision": store.Inspect().SavedRevision, "edits": []any{map[string]any{"path": []string{"address"}, "value": "192.0.2.100"}}}}, false)
	require.Error(t, sup.Reconcile(t.Context(), store.Snapshot().Config().DHCP, store.Snapshot().Generation()))
	status = call("get_dhcp_status", map[string]any{}, false)
	assert.NotEmpty(t, status["dhcp"].(map[string]any)["last_error"])
	call("remove_dhcp_reservation", map[string]any{"id": "printer", "body": map[string]any{"revision": store.Inspect().SavedRevision}}, false)
	require.NoError(t, sup.Reconcile(t.Context(), store.Snapshot().Config().DHCP, store.Snapshot().Generation()))
	job := call("create_job", map[string]any{"body": map[string]any{"kind": "dhcp-check", "input": map[string]any{"probe_other_servers": false}}}, false)
	assert.Equal(t, "running", job["state"])
	require.Eventually(t, func() bool { return len(shared.Jobs()) == 1 && shared.Jobs()[0].State != "running" }, time.Second, time.Millisecond)
	jobs := call("list_jobs", map[string]any{}, false)
	assert.Contains(t, jobs, "items")
	assert.Equal(t, "not_probed", shared.Jobs()[0].Result.(map[string]any)["observation"])
	writer.failed.Store(true)
	require.Eventually(t, func() bool { return sup.Status().State == "degraded" }, time.Second, time.Millisecond)
	status = call("get_dhcp_status", map[string]any{}, false)
	assert.Equal(t, "uncertain", status["dhcp"].(map[string]any)["runtime"].(map[string]any)["storage"])
	leases = call("list_dhcp_leases", map[string]any{}, false)
	require.Len(t, leases["items"], 1, "uncertain storage must not imply released ownership")
	// Expired caller: inspect the coordinator after the failure instead of replaying.
	before := store.Inspect().SavedRevision
	b, e := json.Marshal(map[string]any{"revision": before, "edits": []any{map[string]any{"path": []string{"enabled"}, "value": false}}})
	require.NoError(t, e)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	req := httptest.NewRequest(http.MethodPatch, "http://local/api/v1/dhcp", strings.NewReader(string(b))).WithContext(ctx)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, req)
	assert.Equal(t, 503, response.Code, response.Body.String())
	inspected = call("get_dhcp", map[string]any{}, false)
	assert.Equal(t, before, inspected["status"].(map[string]any)["saved_revision"])
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	assert.True(t, errors.Is(writer.Err(), dhcp.ErrStoreUncertain))
}
