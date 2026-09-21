package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// Validate is shared by file loading and future UI/CLI adapters. Port zero is
// explicitly supported for isolated test runners; hostnames never trigger DNS.
func Validate(c Config) error {
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
	if len(c.DNS.Upstreams) > 16 {
		return fmt.Errorf("dns.upstreams: at most 16 endpoints")
	}
	for i, a := range c.DNS.Upstreams {
		endpoint, err := netip.ParseAddrPort(a)
		if err != nil || endpoint.Port() == 0 || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() {
			return fmt.Errorf("dns.upstreams[%d]: expected unicast literal IP and nonzero port", i)
		}
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
	return nil
}

func address(field, value string) error {
	if _, err := netip.ParseAddrPort(value); err != nil {
		return fmt.Errorf("%s: expected literal IP:port (IPv6 in brackets): %w", field, err)
	}
	return nil
}
