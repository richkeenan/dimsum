package clients

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNegativeDiscoveryRetainsAttemptedSource(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hosts")
	require.NoError(t, os.WriteFile(file, []byte("192.168.1.3 other.home.arpa\n"), 0600))
	for _, tc := range []struct {
		name      string
		settings  Settings
		source    string
		wantError bool
	}{
		{"no-source", Settings{}, "unknown", false},
		{"hosts-miss", Settings{HostsFile: file}, "hosts", false},
		{"hosts-error", Settings{HostsFile: file + ".missing"}, "hosts", true},
		{"ptr-error", Settings{Resolver: "127.0.0.1:53"}, "router-ptr", true},
		{"hosts-then-ptr-error", Settings{HostsFile: file, Resolver: "127.0.0.1:53"}, "router-ptr", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := NewView(tc.settings, nil, nil)
			require.NoError(t, err)
			// Cancellation exercises the PTR error path without sending traffic.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			got := discover(ctx, view, netip.MustParseAddr("192.168.1.2"))
			assert.True(t, got.Negative)
			assert.Empty(t, got.Name)
			assert.Equal(t, tc.source, got.Source)
			assert.Equal(t, tc.wantError, got.Error != "")
		})
	}
}
