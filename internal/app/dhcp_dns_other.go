//go:build !unix

package app

import "net/netip"

// DHCP's system transport is Linux-only. Fail closed when socket capability
// inspection is unavailable; never infer dual-stack support from an address.
func dnsIPv4Address(any) (netip.AddrPort, bool) { return netip.AddrPort{}, false }
