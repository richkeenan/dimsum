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
	for _, item := range []any{"https://dns.example/dns-query", "192.0.2.1:0", map[string]any{"preset": "unknown"}} {
		_, err := appendUpstream(d, item)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "editable shape")
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
