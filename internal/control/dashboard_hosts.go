package control

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/policy"
)

type DashboardHost struct {
	Name string `json:"name"`
	Host string `json:"host"`
	URL  string `json:"url"`
}

// AllowsAdminHost uses only the published configuration, never a pending edit.
func (s *Service) AllowsAdminHost(host string) bool {
	if s.options.Store == nil || s.options.Store.Snapshot() == nil {
		return false
	}
	c := s.options.Store.Snapshot().Config()
	if !strings.Contains(host, ":") {
		port := "80"
		if c.Admin.SecureCookies {
			port = "443"
		}
		host = net.JoinHostPort(host, port)
	}
	for _, allowed := range c.Admin.AllowedHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}

// No external resolution: only exact A/AAAA records and local interfaces qualify.
func dashboardHosts(c config.Config) []DashboardHost {
	// A reverse proxy's public HTTPS endpoint cannot be inferred from our listener.
	if c.Admin.SecureCookies {
		return nil
	}
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	local := make(map[netip.Addr]bool)
	for _, iface := range interfaces {
		if prefix, err := netip.ParsePrefix(iface.String()); err == nil {
			local[prefix.Addr().Unmap()] = true
		}
	}
	bindHost, port, err := net.SplitHostPort(c.Admin.Listen)
	if err != nil || port == "0" {
		return nil
	}
	bind, err := netip.ParseAddr(bindHost)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, host := range c.Admin.AllowedHosts {
		seen[strings.ToLower(host)] = true
	}
	var result []DashboardHost
	ineligible := make(map[string]bool)
	for _, record := range c.Records {
		if (record.Type != "A" && record.Type != "AAAA") || (record.Match != "" && record.Match != "exact") {
			continue
		}
		name, err := policy.NormalizeName(record.Name)
		if err != nil {
			continue
		}
		host := net.JoinHostPort(name.Display(), port)
		ip, err := netip.ParseAddr(record.Value)
		if err != nil || !local[ip.Unmap()] || (!bind.IsUnspecified() && bind.Unmap() != ip.Unmap()) || (bind.Is4() && !ip.Unmap().Is4()) {
			ineligible[host] = true
			continue
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		result = append(result, DashboardHost{Name: name.Display(), Host: host, URL: "http://" + host + "/"})
	}
	eligible := result[:0]
	for _, candidate := range result {
		if !ineligible[candidate.Host] {
			eligible = append(eligible, candidate)
		}
	}
	return eligible
}

func (s *Service) acceptDashboardHost(d *config.Document, name string) (*config.Document, error) {
	normalized, err := policy.NormalizeName(name)
	if err != nil {
		return nil, err
	}
	for _, candidate := range dashboardHosts(d.Config()) {
		if candidate.Name != normalized.Display() {
			continue
		}
		updated, err := d.AddAdminHost(candidate.Host)
		if err != nil {
			return insertMissingField(d, "admin", "allowed_hosts", []string{candidate.Host})
		}
		return updated, nil
	}
	return nil, fmt.Errorf("hostname must be an unaccepted local A/AAAA record pointing to this admin listener")
}
