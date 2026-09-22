//go:build linux

package dhcp

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The parent needs NET_ADMIN to create a disposable veth and SETPCAP to drop
// privileges. Each exec child has only the tested service capabilities, not
// NET_ADMIN. Run exclusively in a network-none container with privileged ports
// restored (net.ipv4.ip_unprivileged_port_start=1024).
func TestLinuxMinimalServiceCapabilities(t *testing.T) {
	if mode := os.Getenv("DIMSUM_DHCP_CAP_CHILD"); mode != "" {
		var caps [2]unix.CapUserData
		require.NoError(t, unix.Capget(&unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}, &caps[0]))
		want := uint32(1<<unix.CAP_NET_RAW | 1<<unix.CAP_NET_BIND_SERVICE)
		if mode == "no-raw" {
			want &^= 1 << unix.CAP_NET_RAW
		}
		if mode == "no-bind" {
			want &^= 1 << unix.CAP_NET_BIND_SERVICE
		}
		require.Equal(t, want, caps[0].Effective)
		require.Zero(t, caps[1].Effective)
		s := fixtureSettings()
		s.Interface = "dhcp-server"
		link, probe, err := OpenSystemLink(s)
		if mode != "minimal" {
			require.Error(t, err)
			assert.True(t, errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES), "expected permission failure: %v", err)
			assert.Nil(t, link)
			return
		}
		require.NoError(t, err)
		defer link.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		conflict, err := probe(ctx, netip.MustParseAddr("192.0.2.101"))
		require.NoError(t, err)
		assert.False(t, conflict)
		wire, err := BuildReply(s, Reply{Type: Offer, Request: Request{MAC: [6]byte{2, 0, 0, 0, 0, 2}, Broadcast: true}, Address: netip.MustParseAddr("192.0.2.100"), LeaseSeconds: 3600}, link.MTU())
		require.NoError(t, err)
		require.NoError(t, link.Send(ctx, wire))
		return
	}
	if os.Getenv("DIMSUM_DHCP_CAPABILITY_TEST") != "1" {
		t.Skip("requires explicit capability-drop fixture with SETPCAP")
	}
	isolatedLink(t)
	ports, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	require.NoError(t, err)
	require.Equal(t, "1024\n", string(ports))
	_, err = exec.LookPath("setpriv")
	require.NoError(t, err)
	for _, tc := range []struct{ mode, bounding string }{
		{"minimal", "-all,+net_raw,+net_bind_service"},
		{"no-raw", "-all,+net_bind_service"},
		{"no-bind", "-all,+net_raw"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "setpriv", "--bounding-set="+tc.bounding, os.Args[0], "-test.run=^TestLinuxMinimalServiceCapabilities$", "-test.v")
			cmd.Env = append(os.Environ(), "DIMSUM_DHCP_CAP_CHILD="+tc.mode)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			t.Log(string(out))
		})
	}
}
