package control

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/richkeenan/dimsum/internal/config"
)

// Presets expand in a single candidate so both addresses activate atomically.
// The same mutation is available through HTTP, MCP and the CLI.
func appendUpstream(d *config.Document, item any) (*config.Document, error) {
	var addresses []string
	switch value := item.(type) {
	case string:
		addresses = []string{value}
	case map[string]any:
		if len(value) != 1 {
			return nil, fmt.Errorf("choose a preset or enter an IP address")
		}
		switch value["preset"] {
		case "cloudflare":
			addresses = []string{"1.1.1.1:53", "1.0.0.1:53"}
		case "google":
			addresses = []string{"8.8.8.8:53", "8.8.4.4:53"}
		case "quad9":
			addresses = []string{"9.9.9.9:53", "149.112.112.112:53"}
		default:
			return nil, fmt.Errorf("unknown upstream preset; choose cloudflare, google or quad9")
		}
	default:
		return nil, fmt.Errorf("enter an IP address or choose an upstream preset")
	}
	seen := map[netip.AddrPort]bool{}
	for _, address := range d.Config().DNS.Upstreams {
		endpoint, err := netip.ParseAddrPort(address)
		if err == nil {
			seen[netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())] = true
		}
	}
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if ip, err := netip.ParseAddr(address); err == nil {
			address = netip.AddrPortFrom(ip, 53).String()
		}
		endpoint, err := netip.ParseAddrPort(address)
		if err != nil || endpoint.Port() == 0 || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() {
			return nil, fmt.Errorf("enter a unicast IP address and a port from 1 to 65535, for example 192.0.2.53:53; URLs and hostnames are not supported")
		}
		endpoint = netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())
		if seen[endpoint] {
			continue
		}
		candidate, err := d.Append([]string{"dns", "upstreams"}, endpoint.String())
		if err != nil && (strings.Contains(err.Error(), "collection edit: missing upstreams") || strings.Contains(err.Error(), "collection edit: missing dns")) {
			candidate, err = insertMissingField(d, "dns", "upstreams", []string{endpoint.String()})
		}
		if err != nil {
			return nil, err
		}
		d = candidate
		seen[endpoint] = true
	}
	return d, nil
}
