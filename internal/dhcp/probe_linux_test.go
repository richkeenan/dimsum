//go:build linux

package dhcp

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/mdlayher/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Exercise the production probe over a real, isolated veth pair. A one-shot
// check misses both a delayed owner and an owner that misses the first packets.
func TestLinuxARPProbeResilience(t *testing.T) {
	isolatedLink(t)
	server, err := net.InterfaceByName("dhcp-server")
	require.NoError(t, err)
	client, err := net.InterfaceByName("dhcp-client")
	require.NoError(t, err)
	target := netip.MustParseAddr("192.0.2.110")
	for _, tc := range []struct {
		name   string
		ignore int
		delay  time.Duration
	}{
		{"delayed-owner", 0, 900 * time.Millisecond},
		{"first-two-probes-lost", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arp, err := packet.Listen(client, packet.Datagram, unix.ETH_P_ARP, nil)
			require.NoError(t, err)
			defer arp.Close()
			require.NoError(t, arp.SetDeadline(time.Now().Add(3*time.Second)))
			done := make(chan error, 1)
			go func() {
				var b [128]byte
				seen := 0
				for {
					n, _, err := arp.ReadFrom(b[:])
					if err != nil {
						done <- err
						return
					}
					if n < 28 || !bytes.Equal(b[8:14], server.HardwareAddr) || !bytes.Equal(b[24:28], target.AsSlice()) {
						continue
					}
					seen++
					if seen <= tc.ignore {
						continue
					}
					time.Sleep(tc.delay)
					// Independently encoded ARP reply claiming the candidate.
					claim := []byte{0, 1, 8, 0, 6, 4, 0, 2, 2, 0, 0, 0, 0, 2, 192, 0, 2, 110, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0}
					_, err = arp.WriteTo(claim, &packet.Addr{HardwareAddr: server.HardwareAddr})
					done <- err
					return
				}
			}()
			conflict, err := probeARP(t.Context(), server, target)
			assert.NoError(t, err)
			assert.True(t, conflict, "must detect owner after delayed/lost probes")
			assert.NoError(t, <-done)
		})
	}
	t.Run("quiet-address-bounded", func(t *testing.T) {
		arp, err := packet.Listen(client, packet.Datagram, unix.ETH_P_ARP, nil)
		require.NoError(t, err)
		defer arp.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		start := time.Now()
		conflict, err := probeARP(ctx, server, target)
		require.NoError(t, err)
		assert.False(t, conflict)
		assert.GreaterOrEqual(t, time.Since(start), 1500*time.Millisecond)
		require.NoError(t, arp.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
		count := 0
		for {
			var b [128]byte
			n, _, err := arp.ReadFrom(b[:])
			if err != nil {
				var timeout net.Error
				require.ErrorAs(t, err, &timeout)
				require.True(t, timeout.Timeout())
				break
			}
			if n >= 28 && bytes.Equal(b[8:14], server.HardwareAddr) && bytes.Equal(b[24:28], target.AsSlice()) {
				count++
			}
		}
		assert.Equal(t, 3, count, "quiet candidate gets repeated, bounded checks")
	})
	t.Run("cancellation-is-not-a-free-address", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
		defer cancel()
		start := time.Now()
		conflict, err := probeARP(ctx, server, target)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.False(t, conflict)
		assert.Less(t, time.Since(start), 500*time.Millisecond)
	})
}
