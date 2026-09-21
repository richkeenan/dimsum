package config

import (
	"fmt"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/upstream"
	"net"
	"net/netip"
	"strings"
)

// Validate is shared by file loading and future UI/CLI adapters. Port zero is
// explicitly supported for isolated test runners; hostnames never trigger DNS.
func Validate(c Config) error {
	if _, err := clients.NewView(c.Naming, c.Clients, nil); err != nil {
		return fmt.Errorf("naming: %w", err)
	}
	if c.Version != 1 {
		return fmt.Errorf("version: unsupported schema %d (want 1)", c.Version)
	}
	if len(c.DNS.Listen) == 0 {
		return fmt.Errorf("dns.listen: at least one address required")
	}
	seen := make(map[string]bool)
	for i, a := range c.DNS.Listen {
		if err := address(fmt.Sprintf("dns.listen[%d]", i), a); err != nil {
			return err
		}
		if seen[a] {
			return fmt.Errorf("dns.listen[%d]: duplicate address %q", i, a)
		}
		seen[a] = true
	}
	if err := address("admin.listen", c.Admin.Listen); err != nil {
		return err
	}
	if len(c.DNS.Upstreams) > 16 || len(c.DNS.Fallback) > 16 {
		return fmt.Errorf("dns.upstreams: at most 16 endpoints")
	}
	endpoints := append([]string(nil), c.DNS.Upstreams...)
	endpoints = append(endpoints, c.DNS.Fallback...)
	if c.Naming.Resolver != "" {
		endpoints = append(endpoints, c.Naming.Resolver)
	}
	for i, a := range endpoints {
		field := fmt.Sprintf("dns.upstreams[%d]", i)
		if i >= len(c.DNS.Upstreams) {
			field = fmt.Sprintf("dns.fallback_upstreams[%d]", i-len(c.DNS.Upstreams))
		}
		if i == len(c.DNS.Upstreams)+len(c.DNS.Fallback) {
			field = "naming.resolver"
		}
		endpoint, err := netip.ParseAddrPort(a)
		if err != nil || endpoint.Port() == 0 || endpoint.Addr().Unmap().IsUnspecified() || endpoint.Addr().Unmap().IsMulticast() {
			return fmt.Errorf("%s: expected unicast literal IP and nonzero port", field)
		}
		for _, listen := range c.DNS.Listen {
			listener, _ := netip.ParseAddrPort(listen) // validated above
			if listener.Port() != endpoint.Port() {
				continue
			}
			listenerAddr := listener.Addr().Unmap()
			local := listenerAddr == endpoint.Addr().Unmap()
			if listenerAddr.IsUnspecified() && (listenerAddr.Is6() || endpoint.Addr().Unmap().Is4()) {
				local = endpoint.Addr().IsLoopback()
				addresses, err := net.InterfaceAddrs()
				if err != nil {
					return fmt.Errorf("%s: check wildcard listener addresses: %w", field, err)
				}
				for _, address := range addresses {
					if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Addr().Unmap() == endpoint.Addr().Unmap().WithZone("") {
						local = true
						break
					}
				}
			}
			if local {
				return fmt.Errorf("%s: endpoint points to DNS listener %s", field, listen)
			}
		}
	}
	p := c.DNS.UpstreamPolicy
	for _, ms := range []int{p.TimeoutMS, p.AttemptTimeoutMS, p.OpenMS, p.MaxBackoffMS} {
		if ms < 0 || ms > 60000 {
			return fmt.Errorf("dns.upstream_policy: durations must be 0..60000 milliseconds")
		}
	}
	o := c.DNS.UpstreamOptions()
	// A listener-only configuration remains valid; forwarding requires a primary.
	if len(o.Endpoints) == 0 {
		if len(o.Fallback) > 0 {
			return fmt.Errorf("dns.upstreams: primary required with fallback")
		}
		o.Endpoints = []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:53")}
	}
	if err := upstream.ValidateOptions(o); err != nil {
		return fmt.Errorf("dns.upstream_policy: %w", err)
	}
	if strings.TrimSpace(c.Paths.DataDir) == "" || strings.TrimSpace(c.Paths.SecretsDir) == "" {
		return fmt.Errorf("paths: data_dir and secrets_dir are required")
	}
	if c.Paths.DataDir == c.Paths.SecretsDir {
		return fmt.Errorf("paths: data and secrets must be separate directories")
	}
	switch c.Cache.StaleMode {
	case "immediate", "failure-only", "off":
	default:
		return fmt.Errorf("cache.stale_mode: expected immediate, failure-only, or off")
	}
	if c.Cache.MaxStaleSeconds < 0 {
		return fmt.Errorf("cache.max_stale_seconds: must be nonnegative")
	}
	return validatePolicy(c)
}

func address(field, value string) error {
	if _, err := netip.ParseAddrPort(value); err != nil {
		return fmt.Errorf("%s: expected literal IP:port (IPv6 in brackets): %w", field, err)
	}
	return nil
}
