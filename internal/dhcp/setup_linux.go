//go:build linux

package dhcp

import (
	"net"
	"os"
)

// DetectSetup reads local interface/route metadata only: no LAN probes, DHCP
// sockets, lease storage or network configuration changes.
func DetectSetup(saved Settings) Setup {
	var routes []setupRoute
	if f, err := os.Open("/proc/net/route"); err == nil {
		routes = parseSetupRoutes(f)
		_ = f.Close()
	}
	var addresses []setupAddress
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		list, _ := iface.Addrs()
		for _, address := range setupAddresses(iface, list, saved.Interface) {
			if !address.SkipSelection {
				if yes, err := permanentAddress(iface.Index, address.Prefix.Addr()); err == nil {
					address.Fixed = "no"
					if yes {
						address.Fixed = "yes"
					}
				}
			}
			addresses = append(addresses, address)
		}
	}
	return suggestSetup(saved, addresses, routes)
}
