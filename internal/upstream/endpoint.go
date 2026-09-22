package upstream

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Endpoint is a validated configured identity, independent of bootstrap addresses.
type Endpoint struct {
	identity, host, transport string
	port                      uint16
	literal                   netip.Addr
}

func (e Endpoint) String() string               { return e.identity }
func (e Endpoint) MarshalText() ([]byte, error) { return []byte(e.identity), nil }
func (e Endpoint) Transport() string            { return e.transport }
func (e Endpoint) Addr() netip.Addr             { return e.literal }
func (e Endpoint) Port() uint16                 { return e.port }
func (e Endpoint) IsValid() bool                { return e.identity != "" }
func PlainEndpoint(a netip.AddrPort) Endpoint {
	e, _ := ParseEndpoint(a.String())
	return e
}

func ParseEndpoint(s string) (Endpoint, error) {
	bad := func() (Endpoint, error) { return Endpoint{}, fmt.Errorf("upstream: invalid endpoint %q", s) }
	if s == "" || strings.TrimSpace(s) != s {
		return bad()
	}
	if !strings.Contains(s, "://") {
		a, err := netip.ParseAddrPort(s)
		if err != nil || a.Port() == 0 || !unicast(a.Addr()) {
			return bad()
		}
		return Endpoint{s, a.Addr().String(), "udp", a.Port(), a.Addr()}, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || strings.Contains(s, "#") || u.Host == "" {
		return bad()
	}
	port := uint64(443)
	switch u.Scheme {
	case "tls":
		port = 853
		if u.Path != "" || u.RawQuery != "" || u.ForceQuery {
			return bad()
		}
	case "https":
		if u.Path == "" || !strings.HasPrefix(u.Path, "/") {
			return bad()
		}
	default:
		return bad()
	}
	if strings.HasSuffix(u.Host, ":") {
		return bad()
	}
	if u.Port() != "" {
		port, err = strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || port == 0 {
			return bad()
		}
	}
	host := u.Hostname()
	a, err := netip.ParseAddr(host)
	if err == nil {
		if !unicast(a) || a.Zone() != "" {
			return bad()
		}
	} else {
		if len(host) > 253 || strings.Contains(host, ":") {
			return bad()
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return bad()
			}
			for _, ch := range label {
				if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
					return bad()
				}
			}
		}
	}
	transport := "doh"
	if u.Scheme == "tls" {
		transport = "dot"
	}
	return Endpoint{s, host, transport, uint16(port), a}, nil
}
func unicast(a netip.Addr) bool {
	return a.IsValid() && !a.Unmap().IsUnspecified() && !a.Unmap().IsMulticast()
}
