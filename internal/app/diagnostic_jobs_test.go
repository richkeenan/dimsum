package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/richkeenan/dimsum/internal/upstream"
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

func TestDiagnosticEncryptedProbeUsesBootstrapAndReportsTLSFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted HTTPS server must not receive a DNS request")
	}))
	defer server.Close()
	for _, scheme := range []string{"https", "tls"} {
		t.Run(scheme, func(t *testing.T) {
			endpoint := strings.Replace(server.URL, "https://", scheme+"://", 1)
			if scheme == "https" {
				endpoint += "/dns-query"
			}
			m, _ := diagnosticFixture(t, endpoint, true)
			value, err := m.upstreamProbe(t.Context(), json.RawMessage(fmt.Sprintf(`{"endpoint":%q}`, endpoint)))
			require.NoError(t, err)
			result := value.(upstreamProbeResult)
			assert.Equal(t, scheme, result.Transport)
			assert.False(t, result.Responding)
			assert.False(t, result.Healthy)
			assert.Contains(t, result.Error, "certificate")
			assert.Equal(t, endpoint, result.Endpoint)
		})
	}
	bootstrap, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return diagnosticReply(r, 3, false) })
	require.NoError(t, err)
	defer bootstrap.Close()
	endpoint := "https://probe.example/dns-query"
	m, _ := diagnosticFixture(t, endpoint, false)
	_, err = m.control.Mutate(t.Context(), "settings", "PATCH", control.Mutation{Revision: m.store.Snapshot().Revision(), Edits: []config.Edit{{Path: []string{"dns", "bootstrap_dns"}, Value: []any{bootstrap.Address()}}}})
	require.NoError(t, err)
	value, err := m.upstreamProbe(t.Context(), json.RawMessage(fmt.Sprintf(`{"endpoint":%q}`, endpoint)))
	require.NoError(t, err)
	result := value.(upstreamProbeResult)
	assert.Equal(t, "https", result.Transport)
	assert.False(t, result.Responding)
	assert.Contains(t, result.Error, "bootstrap")
	select {
	case request := <-bootstrap.Requests():
		var parsed dnswire.Message
		require.NoError(t, dnswire.ParseRequest(request.Wire, &parsed))
		assert.Contains(t, []uint16{1, 28}, parsed.Question.Type)
	default:
		t.Fatal("probe ignored configured bootstrap")
	}
}

func TestDiagnosticHTTPSProbeRequiresValidatedDNS(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if len(wire) != 17 {
					t.Errorf("unexpected query length %d", len(wire))
					return
				}
				wire[2] |= 0x80
				if mismatch {
					wire[len(wire)-3] = 1
				}
				w.Header().Set("Content-Type", "application/dns-message")
				_, _ = w.Write(wire)
			}))
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			endpoint, err := upstream.ParseEndpoint(server.URL + "/dns-query")
			require.NoError(t, err)
			m := &managedRuntime{}
			value, err := m.probeEndpoint(t.Context(), endpoint, upstream.Options{RootCAs: roots}, t.Context(), 7, 500)
			require.NoError(t, err)
			result := value.(upstreamProbeResult)
			assert.Equal(t, "https", result.Transport)
			assert.Equal(t, "7", result.Generation)
			assert.Equal(t, !mismatch, result.Healthy)
			assert.Equal(t, !mismatch, result.Responding)
			if mismatch {
				assert.NotEmpty(t, result.Error)
			} else {
				assert.Equal(t, "healthy", result.State)
			}
		})
	}
}

func TestDiagnosticHTTPSProbeRetiresWithTransportLifetime(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	endpoint, err := upstream.ParseEndpoint(server.URL + "/dns-query")
	require.NoError(t, err)
	lifetime, retire := context.WithCancel(t.Context())
	defer retire()
	done := make(chan error, 1)
	go func() {
		_, err := (&managedRuntime{}).probeEndpoint(t.Context(), endpoint, upstream.Options{RootCAs: roots}, lifetime, 1, 3000)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not reach HTTPS fixture")
	}
	retire()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("retired probe kept HTTPS request open")
	}
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("retired probe did not finish")
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
