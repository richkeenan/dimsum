package admin_test

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptedUpstreamsAndBootstrapSharedAPI(t *testing.T) {
	service, store, _ := fixture(t)
	t.Cleanup(service.Close)
	h := admin.New(service, admin.Options{}).LocalHandler()
	body := mutation(t, store, map[string]any{"item": map[string]any{"preset": "google", "transport": "https"}})
	w := request(h, "POST", "/api/v1/upstreams", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"https://dns.google/dns-query"}, store.Snapshot().Config().DNS.Upstreams)
	w = request(h, "POST", "/api/v1/upstreams", body)
	assert.Equal(t, 409, w.Code)
	w = request(h, "PATCH", "/api/v1/upstreams", mutation(t, store, map[string]any{"edits": []any{map[string]any{"path": []string{"0"}, "value": "tls://dns.example:8853"}}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"tls://dns.example:8853"}, store.Snapshot().Config().DNS.Upstreams)
	for _, values := range [][]string{{"192.0.2.53:53"}, {"1.1.1.1:53", "9.9.9.9:53"}} {
		w = request(h, "PATCH", "/api/v1/settings", mutation(t, store, map[string]any{"edits": []any{map[string]any{"path": []string{"dns", "bootstrap_dns"}, "value": values}}}))
		require.Equal(t, 200, w.Code, w.Body.String())
		assert.Equal(t, values, store.Snapshot().Config().DNS.BootstrapDNS)
		w = request(h, "GET", "/api/v1/settings", "")
		assert.Contains(t, w.Body.String(), values[0])
	}
	revision := store.Inspect().SavedRevision
	w = request(h, "PATCH", "/api/v1/settings", mutation(t, store, map[string]any{"edits": []any{map[string]any{"path": []string{"dns", "bootstrap_dns"}, "value": []string{}}}}))
	assert.Equal(t, 422, w.Code, w.Body.String())
	assert.Equal(t, revision, store.Inspect().SavedRevision)
}
