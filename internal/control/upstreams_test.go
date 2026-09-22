package control

import (
	"fmt"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamPresetAddsOnlyMissingServers(t *testing.T) {
	d, err := config.Parse([]byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [1.1.1.1:53] # keep\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"))
	require.NoError(t, err)
	added, err := appendUpstream(d, map[string]any{"preset": "cloudflare"})
	require.NoError(t, err)
	assert.Equal(t, []string{"1.1.1.1:53", "1.0.0.1:53"}, added.Config().DNS.Upstreams)
	assert.Contains(t, string(added.Bytes()), "# keep")
	again, err := appendUpstream(added, map[string]any{"preset": "cloudflare"})
	require.NoError(t, err)
	assert.Equal(t, added.Bytes(), again.Bytes())
	custom, err := appendUpstream(added, "2001:db8::53")
	require.NoError(t, err)
	assert.Equal(t, "[2001:db8::53]:53", custom.Config().DNS.Upstreams[2])
	for _, item := range []any{"http://dns.example/dns-query", "192.0.2.1:0", map[string]any{"preset": "unknown"}} {
		_, err := appendUpstream(d, item)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "editable shape")
	}
}

func TestEncryptedUpstreamMutations(t *testing.T) {
	d, err := config.Parse([]byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [192.0.2.53:53] # keep\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"))
	require.NoError(t, err)
	for _, tc := range []struct{ preset, endpoint string }{{"cloudflare", "https://cloudflare-dns.com/dns-query"}, {"google", "https://dns.google/dns-query"}, {"quad9", "https://dns.quad9.net/dns-query"}} {
		t.Run(tc.preset, func(t *testing.T) {
			next, err := appendUpstream(d, map[string]any{"preset": tc.preset, "transport": "https"})
			require.NoError(t, err)
			assert.Equal(t, []string{"192.0.2.53:53", tc.endpoint}, next.Config().DNS.Upstreams)
			again, err := appendUpstream(next, "  "+tc.endpoint+"  ")
			require.NoError(t, err)
			assert.Equal(t, next.Bytes(), again.Bytes())
			plain, err := appendUpstream(d, map[string]any{"preset": tc.preset, "transport": "plain"})
			require.NoError(t, err)
			legacy, err := appendUpstream(d, map[string]any{"preset": tc.preset})
			require.NoError(t, err)
			assert.Equal(t, legacy.Bytes(), plain.Bytes())
		})
	}
	next, err := appendUpstream(d, "tls://dns.example:8853")
	require.NoError(t, err)
	assert.Equal(t, "tls://dns.example:8853", next.Config().DNS.Upstreams[1])
	assert.Contains(t, string(next.Bytes()), "# keep")
	again, err := appendUpstream(d, "[::ffff:192.0.2.53]:53")
	require.NoError(t, err)
	assert.Equal(t, d.Bytes(), again.Bytes())
	for _, bad := range []any{"https://dns.example", "https://user:pass@dns.example/query", "tls://dns.example/path", "https://dns.example/query#fragment", map[string]any{"preset": "google", "transport": "tls"}, map[string]any{"preset": "google", "transport": nil}, map[string]any{"preset": "google", "extra": true}} {
		_, err := appendUpstream(d, bad)
		assert.Error(t, err, "%v", bad)
	}
}

func TestPresetFailureDoesNotLeaveFirstServerAdded(t *testing.T) {
	var addresses []string
	for i := 1; i <= 15; i++ {
		addresses = append(addresses, fmt.Sprintf("192.0.2.%d:53", i))
	}
	source := "version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [" + strings.Join(addresses, ", ") + "]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"
	d, err := config.Parse([]byte(source))
	require.NoError(t, err)
	candidate, err := appendUpstream(d, map[string]any{"preset": "google"})
	require.ErrorContains(t, err, "at most 16")
	assert.Nil(t, candidate)
	assert.Equal(t, source, string(d.Bytes()))
	assert.Equal(t, addresses, d.Config().DNS.Upstreams)
}
