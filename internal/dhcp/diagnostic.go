package dhcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

type ServerProbeResult struct {
	Servers   []string `json:"servers"`
	Truncated bool     `json:"truncated"`
}

// ProbeServers sends exactly one DISCOVER, never a REQUEST. A quiet observation
// cannot establish absence of other servers. Exclusive port 68 avoids sharing
// packets with an existing host DHCP client. No lease/runtime storage is opened.
func ProbeServers(ctx context.Context, settings Settings, timeout time.Duration) (ServerProbeResult, error) {
	if err := CurrentAvailability().Check(); err != nil {
		return ServerProbeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ServerProbeResult{}, err
	}
	if timeout < 100*time.Millisecond || timeout > 3*time.Second {
		return ServerProbeResult{}, fmt.Errorf("probe timeout must be 100..3000 ms")
	}
	conn, err := openDiagnosticSocket(settings.Interface)
	if err != nil {
		return ServerProbeResult{}, err
	}
	defer conn.Close()
	var mac [6]byte
	if _, err = rand.Read(mac[:]); err != nil {
		return ServerProbeResult{}, err
	}
	mac[0] = (mac[0] | 2) & 0xfe
	discovery, err := dhcpv4.NewDiscovery(mac[:], dhcpv4.WithBroadcast(true))
	if err != nil {
		return ServerProbeResult{}, err
	}
	return observeServers(ctx, conn, discovery, settings.ServerIP, timeout)
}

type diagnosticSocket interface {
	SetDeadline(time.Time) error
	WriteToUDPAddrPort([]byte, netip.AddrPort) (int, error)
	ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error)
}

func observeServers(ctx context.Context, conn diagnosticSocket, discovery *dhcpv4.DHCPv4, own string, timeout time.Duration) (ServerProbeResult, error) {
	result := ServerProbeResult{Servers: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return result, err
	}
	if _, err := conn.WriteToUDPAddrPort(discovery.ToBytes(), netip.MustParseAddrPort("255.255.255.255:67")); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	var buf [MaxDatagram + 1]byte
	packets := 0
	for ; packets < 64; packets++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := conn.SetDeadline(minTime(deadline, time.Now().Add(50*time.Millisecond))); err != nil {
			return result, err
		}
		n, peer, err := conn.ReadFromUDPAddrPort(buf[:])
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				if time.Now().Before(deadline) {
					packets--
					continue
				}
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				break
			}
			return result, err
		}
		if n > MaxDatagram || peer.Port() != 67 {
			continue
		}
		p, err := dhcpv4.FromBytes(buf[:n])
		if err != nil || p.OpCode != dhcpv4.OpcodeBootReply || p.TransactionID != discovery.TransactionID || !bytes.Equal(p.ClientHWAddr, discovery.ClientHWAddr) || p.MessageType() != dhcpv4.MessageTypeOffer {
			continue
		}
		ip, ok := netip.AddrFromSlice(p.ServerIdentifier())
		if !ok || !ip.Unmap().Is4() || ip.IsUnspecified() {
			continue
		}
		server := ip.Unmap().String()
		if server != own && !seen[server] {
			seen[server] = true
			result.Servers = append(result.Servers, server)
		}
		if len(result.Servers) == 16 {
			result.Truncated = true
			break
		}
	}
	if packets == 64 {
		result.Truncated = true
	}
	sort.Strings(result.Servers)
	return result, nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
