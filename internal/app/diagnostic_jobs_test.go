package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diagnosticFixture(t *testing.T, endpoint string, fallback bool) (*managedRuntime, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	upstreamKey := "upstreams"
	if fallback {
		upstreamKey = "upstreams: [\"192.0.2.53:53\"]\n  fallback_upstreams"
	}
	source := fmt.Sprintf(`# Keep operator-owned settings.
version: 1
dns:
  listen: ["127.0.0.1:0"]
  %s: [%q]
admin:
  listen: "127.0.0.1:0"
paths:
  data_dir: "./data"
  secrets_dir: "./secrets"
lists:
  - id: private-source
    url: "https://example.invalid/list?token=source-credential#private-fragment"
    enabled: false
    dialect: domains
    domain_kind: exact
`, upstreamKey, endpoint)
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	m := &managedRuntime{store: store, observations: &observability{collector: stats.New(8)}}
	m.control = control.New(control.Options{Store: store, ConfigPath: path, Jobs: map[string]func(context.Context, json.RawMessage) (any, error){"upstream-probe": m.upstreamProbe, "support-bundle": m.supportBundle}, Diagnostics: func(context.Context) (any, error) {
		return map[string]any{"process": safeJSON(m.observations.collector.Snapshot()), "error": "Get https://user:password@example.invalid/status?key=diagnostic-credential: failed"}, nil
	}})
	t.Cleanup(m.control.Close)
	return m, path
}

func diagnosticReply(r testutil.Request, rcode byte, truncate bool) testutil.Response {
	wire := append([]byte(nil), r.Wire...)
	wire[2] |= 0x80
	if truncate && r.Network == "udp" {
		wire[2] |= 2
	}
	wire[3] = (wire[3] & 0xf0) | rcode
	return testutil.Response{Wire: wire}
}

func TestDiagnosticProbeValidatedLoopbackPrimaryFallbackAndTCP(t *testing.T) {
	for _, mode := range []string{"primary", "fallback", "tcp"} {
		t.Run(mode, func(t *testing.T) {
			server, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return diagnosticReply(r, 0, mode == "tcp") })
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, server.Close()) })
			m, path := diagnosticFixture(t, server.Address(), mode == "fallback")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			value, err := m.upstreamProbe(t.Context(), json.RawMessage(fmt.Sprintf(`{"endpoint":%q}`, server.Address())))
			require.NoError(t, err)
			result := value.(upstreamProbeResult)
			assert.True(t, result.Healthy)
			assert.True(t, result.Responding)
			assert.Equal(t, "healthy", result.State)
			require.NotNil(t, result.RCode)
			assert.Zero(t, *result.RCode)
			assert.Equal(t, "1", result.Generation)
			attempts := uint64(1)
			transport := "udp"
			if mode == "tcp" {
				attempts = 2
				transport = "tcp"
			}
			assert.Equal(t, transport, result.Transport)
			assert.Equal(t, fmt.Sprint(attempts), result.Attempts)
			assert.Equal(t, result.Attempts, result.DiagnosticProbes)
			snapshot := m.observations.collector.Snapshot()
			assert.Equal(t, attempts, snapshot.Attempts)
			assert.Equal(t, attempts, snapshot.HealthProbes)
			assert.Zero(t, snapshot.Admitted)
			assert.Zero(t, snapshot.Sequence)
			assert.Zero(t, snapshot.BackgroundRefreshes)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			assert.Equal(t, uint64(1), m.store.Snapshot().Generation())
			select {
			case request := <-server.Requests():
				var parsed dnswire.Message
				require.NoError(t, dnswire.ParseRequest(request.Wire, &parsed))
				assert.Equal(t, uint16(2), parsed.Question.Type)
				assert.Equal(t, uint16(1), parsed.Question.Class)
				assert.Equal(t, uint16(1), parsed.Question.Name.Length)
			case <-time.After(time.Second):
				require.FailNow(t, "probe did not reach fixture")
			}
		})
	}
}

func TestDiagnosticProbeRejectsUnconfiguredEndpointsWithoutSending(t *testing.T) {
	server, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return diagnosticReply(r, 0, false) })
	require.NoError(t, err)
	defer server.Close()
	m, _ := diagnosticFixture(t, server.Address(), false)
	for _, input := range []string{`{"endpoint":"8.8.8.8:53"}`, `{"endpoint":"example.org:53"}`, `{"endpoint":"127.0.0.1:1"}`, fmt.Sprintf(`{"endpoint":%q,"timeout_ms":3001}`, server.Address()), fmt.Sprintf(`{"endpoint":%q,"name":"private.example"}`, server.Address())} {
		_, err = m.upstreamProbe(t.Context(), json.RawMessage(input))
		assert.ErrorIs(t, err, control.BadRequest)
	}
	assert.Zero(t, m.observations.collector.Snapshot().Attempts)
	select {
	case <-server.Requests():
		t.Fatal("invalid probe sent a packet")
	default:
	}
}

func TestDiagnosticProbeRejectsMismatchedDNSResponse(t *testing.T) {
	server, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		reply := diagnosticReply(r, 0, false)
		reply.Wire[len(reply.Wire)-3] = 1
		return reply
	})
	require.NoError(t, err)
	defer server.Close()
	m, _ := diagnosticFixture(t, server.Address(), false)
	value, err := m.upstreamProbe(t.Context(), json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"timeout_ms":50}`, server.Address())))
	require.NoError(t, err)
	result := value.(upstreamProbeResult)
	assert.Equal(t, "no_valid_response", result.State)
	assert.False(t, result.Responding)
	assert.False(t, result.Healthy)
	assert.Nil(t, result.RCode)
}

func TestDiagnosticProbeResponseErrorsCancellationTimeoutAndQueue(t *testing.T) {
	for _, rcode := range []byte{2, 5} {
		t.Run(fmt.Sprint(rcode), func(t *testing.T) {
			server, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return diagnosticReply(r, rcode, false) })
			require.NoError(t, err)
			defer server.Close()
			m, _ := diagnosticFixture(t, server.Address(), false)
			v, err := m.upstreamProbe(t.Context(), json.RawMessage(fmt.Sprintf(`{"endpoint":%q}`, server.Address())))
			require.NoError(t, err)
			r := v.(upstreamProbeResult)
			assert.Equal(t, "responding_error", r.State)
			assert.True(t, r.Responding)
			assert.False(t, r.Healthy)
			require.NotNil(t, r.RCode)
			assert.Equal(t, uint16(rcode), *r.RCode)
		})
	}
	server, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, err)
	defer server.Close()
	m, _ := diagnosticFixture(t, server.Address(), false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	input := json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"timeout_ms":3000}`, server.Address()))
	go func() { _, err := m.upstreamProbe(ctx, input); done <- err }()
	select {
	case <-server.Requests():
	case <-time.After(time.Second):
		require.FailNow(t, "probe did not start")
	}
	cancel()
	select {
	case err = <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.FailNow(t, "cancelled probe stalled")
	}
	job, err := m.control.StartJob(t.Context(), "upstream-probe", json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"timeout_ms":500}`, server.Address())))
	require.NoError(t, err)
	_, err = m.control.StartJob(t.Context(), "support-bundle", nil)
	assert.ErrorIs(t, err, control.ErrBusy)
	require.Eventually(t, func() bool { return m.control.Jobs()[0].State != "running" }, time.Second, time.Millisecond)
	completed := m.control.Jobs()[0]
	assert.Equal(t, job.ID, completed.ID)
	assert.Equal(t, "succeeded", completed.State)
	result := completed.Result.(upstreamProbeResult)
	assert.Equal(t, "no_valid_response", result.State)
	assert.False(t, result.Responding)
	assert.False(t, result.Healthy)
	assert.NotEmpty(t, result.Error)
}

func TestDiagnosticSupportBundleRedactionSizeAndCancellation(t *testing.T) {
	m, path := diagnosticFixture(t, "192.0.2.53:53", false)
	secret := filepath.Join(filepath.Dir(path), "secrets")
	require.NoError(t, os.Mkdir(secret, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(secret, "admin.hash"), []byte("admin-password-hash-must-never-appear"), 0600))
	value, err := m.supportBundle(t.Context(), nil)
	require.NoError(t, err)
	payload := value.(json.RawMessage)
	assert.LessOrEqual(t, len(payload), maxSupportBundleBytes)
	for _, forbidden := range []string{"admin-password-hash-must-never-appear", "source-credential", "private-fragment", "diagnostic-credential", "user:password"} {
		assert.NotContains(t, string(payload), forbidden)
	}
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(payload, &bundle))
	assert.Equal(t, "dimsum-support-v1", bundle["format"])
	assert.Equal(t, false, bundle["contains_secrets"])
	assert.Equal(t, false, bundle["contains_query_history"])
	assert.Nil(t, bundle["query_history"])
	assert.NotEmpty(t, bundle["build"])
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = m.supportBundle(ctx, nil)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = m.supportBundle(t.Context(), json.RawMessage(`{"include_query_history":true}`))
	assert.ErrorIs(t, err, control.BadRequest)
	_, err = m.supportBundle(t.Context(), json.RawMessage(`{"from":"2026-09-21T00:00:00Z"}`))
	assert.ErrorIs(t, err, control.BadRequest)
	m.control = control.New(control.Options{Store: m.store, ConfigPath: path, Diagnostics: func(context.Context) (any, error) {
		return map[string]any{"bounded_test": strings.Repeat("x", maxSupportBundleBytes)}, nil
	}})
	value, err = m.supportBundle(t.Context(), nil)
	require.Error(t, err)
	assert.Nil(t, value)
	assert.Contains(t, err.Error(), "256 KiB")
}

func TestDiagnosticSupportHistoryRequiresExplicitBoundedOptIn(t *testing.T) {
	h, _ := historyFixture(t)
	require.NoError(t, h.db.WriteBatch(t.Context(), "support-test", []stats.QueryEvent{historyEvent(1, stats.PolicyBlock)}))
	m, path := diagnosticFixture(t, "192.0.2.53:53", false)
	m.control = control.New(control.Options{Store: m.store, ConfigPath: path, Provider: h})
	value, err := m.supportBundle(t.Context(), nil)
	require.NoError(t, err)
	assert.NotContains(t, string(value.(json.RawMessage)), "ads.example")
	input := json.RawMessage(fmt.Sprintf(`{"include_query_history":true,"from":%q,"to":%q,"limit":1}`, historyStart.Format(time.RFC3339), historyEnd.Format(time.RFC3339)))
	value, err = m.supportBundle(t.Context(), input)
	require.NoError(t, err)
	assert.Contains(t, string(value.(json.RawMessage)), "ads.example")
	assert.Contains(t, string(value.(json.RawMessage)), `"contains_query_history":true`)
	_, err = m.supportBundle(t.Context(), json.RawMessage(fmt.Sprintf(`{"include_query_history":true,"from":%q,"to":%q}`, historyStart.Add(-24*time.Hour).Format(time.RFC3339), historyEnd.Format(time.RFC3339))))
	assert.ErrorIs(t, err, control.BadRequest)
	// The export is the same bounded page shape used by regular history queries.
	var bundle struct {
		History historyQueries `json:"query_history"`
	}
	require.NoError(t, json.Unmarshal(value.(json.RawMessage), &bundle))
	expected, err := h.Queries(t.Context(), url.Values{"from": {historyStart.Format(time.RFC3339)}, "to": {historyEnd.Format(time.RFC3339)}, "limit": {"1"}})
	require.NoError(t, err)
	assert.Equal(t, expected.(historyQueries).Items, bundle.History.Items)
}
