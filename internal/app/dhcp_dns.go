package app

import "net"

// dhcpIPv4Listeners inspects the paired bound DNS sockets. Their displayed
// address alone cannot distinguish dual-stack [::] from an IPv6-only listener.
// Keep these endpoints separate from the public/configuration listener list.
func dhcpIPv4Listeners(tcp []net.Listener, udp []net.PacketConn) []string {
	var addresses []string
	for i, listener := range tcp {
		if i >= len(udp) {
			break
		}
		t, tcpOK := dnsIPv4Address(listener)
		u, udpOK := dnsIPv4Address(udp[i])
		if tcpOK && udpOK && t == u {
			addresses = append(addresses, t.String())
		}
	}
	return addresses
}
