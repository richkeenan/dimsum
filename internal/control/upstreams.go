package control

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/upstream"
)

// Presets expand in a single candidate so both addresses activate atomically.
// The same mutation is available through HTTP, MCP and the CLI.
func appendUpstream(d *config.Document, item any) (*config.Document, error) {
	var addresses []string
	switch value := item.(type) {
	case string:
		addresses = []string{value}
	case map[string]any:
		for key := range value {
			if key != "preset" && key != "transport" {
				return nil, fmt.Errorf("choose a preset and optional transport")
			}
		}
		transport := "plain"
		if v, exists := value["transport"]; exists {
			if v != "plain" && v != "https" {
				return nil, fmt.Errorf("preset transport must be https or plain")
			}
			transport = v.(string)
		}
		var encrypted string
		switch value["preset"] {
		case "cloudflare":
			addresses = []string{"1.1.1.1:53", "1.0.0.1:53"}
			encrypted = "https://cloudflare-dns.com/dns-query"
		case "google":
			addresses = []string{"8.8.8.8:53", "8.8.4.4:53"}
			encrypted = "https://dns.google/dns-query"
		case "quad9":
			addresses = []string{"9.9.9.9:53", "149.112.112.112:53"}
			encrypted = "https://dns.quad9.net/dns-query"
		default:
			return nil, fmt.Errorf("unknown upstream preset; choose cloudflare, google or quad9")
		}
		if transport == "https" {
			addresses = []string{encrypted}
		}
	default:
		return nil, fmt.Errorf("enter an IP address, HTTPS/TLS URL or choose an upstream preset")
	}
	seen := map[string]bool{}
	for _, address := range d.Config().DNS.Upstreams {
		endpoint, err := canonicalUpstream(address)
		if err == nil {
			seen[endpoint.String()] = true
		}
	}
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if ip, err := netip.ParseAddr(address); err == nil {
			address = netip.AddrPortFrom(ip, 53).String()
		}
		endpoint, err := canonicalUpstream(address)
		if err != nil {
			return nil, fmt.Errorf("enter a unicast IP:port, https://host/path or tls://host[:port]: %w", err)
		}
		if seen[endpoint.String()] {
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
		seen[endpoint.String()] = true
	}
	return d, nil
}

func canonicalUpstream(address string) (upstream.Endpoint, error) {
	if ap, err := netip.ParseAddrPort(address); err == nil {
		address = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String()
	}
	return upstream.ParseEndpoint(address)
}
