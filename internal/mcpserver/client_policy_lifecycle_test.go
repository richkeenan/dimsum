package mcpserver_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercises the shared HTTP/MCP transaction boundary with downloaded membership
// and actual DNS wire answers, including a backup restored after identity edits.
func TestClientPolicySubscriptionResetRestoreAndRelink(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("adult.example\n"))
	}))
	defer feed.Close()
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var q dns.Msg
		if q.Unpack(r.Wire) != nil {
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg).SetReply(&q)
		m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: 1, Ttl: 60}, A: net.ParseIP("192.0.2.80")}}
		wire, err := m.Pack()
		return testutil.Response{Wire: wire, Drop: err != nil}
	})
	require.NoError(t, err)
	t.Cleanup(func() { u.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	source := fmt.Sprintf("# portable policy identity\nversion: 1\ndns:\n  listen: [127.0.0.1:5353]\n  upstreams: ['%s']\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\n", u.Address())
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Fetcher: lists.NewFetcher(feed.Client())})
	require.NoError(t, err)
	service := control.New(control.Options{Store: store, ConfigPath: path})
	handler := admin.New(service, admin.Options{}).LocalHandler()
	session := connect(t, handler)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	pipeline := resolve.NewWithStore(nil, store)
	t.Cleanup(func() { pipeline.Close() })
	run := func(name string, args any) map[string]any {
		t.Helper()
		result := call(t, session, name, args)
		content, err := json.Marshal(result.Content)
		require.NoError(t, err)
		require.False(t, result.IsError, "%s", content)
		b, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		var value map[string]any
		require.NoError(t, json.Unmarshal(b, &value))
		return value
	}
	mutate := func(body map[string]any) {
		t.Helper()
		body["revision"] = store.Inspect().SavedRevision
		result := run("update_client_policy", map[string]any{"body": body})
		assert.Equal(t, result["saved_revision"], result["active_revision"])
		assert.Equal(t, false, result["pending"])
		assert.Empty(t, result["error"])
	}
	answer := func(address, want string) {
		t.Helper()
		q := new(dns.Msg).SetQuestion("adult.example.", dns.TypeA)
		wire, err := q.Pack()
		require.NoError(t, err)
		r := transport.Request{Wire: wire, Peer: netip.MustParseAddrPort(address + ":12345")}
		require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
		out := make([]byte, 1232)
		n, err := pipeline.Resolve(t.Context(), &r, out)
		require.NoError(t, err)
		var reply dns.Msg
		require.NoError(t, reply.Unpack(out[:n]))
		require.Len(t, reply.Answer, 1)
		a, ok := reply.Answer[0].(*dns.A)
		require.True(t, ok)
		assert.Equal(t, want, a.A.String())
		assert.Equal(t, store.Snapshot().Generation(), r.Result.Generation)
	}
	// Subscription and client assignment are one MCP mutation. No global default.
	mutate(map[string]any{"scope": "client", "id": "phone", "create": true,
		"selectors": map[string]any{"addresses": []string{"192.0.2.10"}},
		"subscribe": []any{map[string]any{"id": "adult", "url": feed.URL, "dialect": "domains", "domain_kind": "suffix", "enabled": true}},
		"fields":    []any{map[string]any{"path": []string{"lists", "adult"}, "value": true}}})
	require.Len(t, store.Inspect().Sources, 1)
	assert.True(t, store.Inspect().Sources[0].Usable)
	require.NotNil(t, store.Snapshot().Config().Lists[0].DefaultApply)
	assert.False(t, *store.Snapshot().Config().Lists[0].DefaultApply)
	answer("192.0.2.10", "0.0.0.0")
	answer("192.0.2.11", "192.0.2.80")
	// A real HTTP profile mutation is immediately visible through MCP and DNS.
	body, err := json.Marshal(map[string]any{"revision": store.Inspect().SavedRevision, "scope": "profile", "id": "children", "create": true,
		"fields": []any{map[string]any{"path": []string{"lists", "adult"}, "value": true}}})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), "PATCH", httpServer.URL+"/api/v1/client-policy", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpServer.Client().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	mutate(map[string]any{"scope": "client", "id": "phone", "profile": "children", "reset_all": true})
	read := run("get_client_policy", map[string]any{"scope": "client", "id": "phone"})
	effective := read["active"].(map[string]any)
	assert.Equal(t, "children", effective["profile_id"])
	assert.EqualValues(t, 0, effective["override_count"])
	answer("192.0.2.10", "0.0.0.0")
	backup, err := store.Backup()
	require.NoError(t, err)
	// Reset follows later profile edits.
	mutate(map[string]any{"scope": "profile", "id": "children", "fields": []any{map[string]any{"path": []string{"lists", "adult"}, "value": false}}})
	answer("192.0.2.10", "192.0.2.80")
	mutate(map[string]any{"scope": "client", "id": "phone", "selectors": map[string]any{"addresses": []string{"192.0.2.20"}}})
	restored, err := store.Restore(t.Context(), store.Inspect().SavedRevision, backup)
	require.NoError(t, err)
	assert.Equal(t, restored.SavedRevision, restored.ActiveRevision)
	answer("192.0.2.10", "0.0.0.0")
	answer("192.0.2.20", "192.0.2.80")
	read = run("get_client_policy", map[string]any{"scope": "client", "id": "phone"})
	assert.Equal(t, effective, read["active"], "restore reinstates the entire effective policy")
	mutate(map[string]any{"scope": "client", "id": "phone", "selectors": map[string]any{"addresses": []string{"192.0.2.20"}}})
	answer("192.0.2.10", "192.0.2.80")
	answer("192.0.2.20", "0.0.0.0")
	answer("192.0.2.11", "192.0.2.80")
	explanation := run("explain_domain", map[string]any{"body": map[string]any{"name": "adult.example", "address": "192.0.2.20"}})
	assert.Equal(t, "phone", explanation["client_id"])
	assert.Equal(t, "address", explanation["matching_method"])
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(saved), "# portable policy identity")
}
