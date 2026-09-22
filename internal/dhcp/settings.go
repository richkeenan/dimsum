package dhcp

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

const DefaultMaxLeases = 1024
const MaximumLeases = 4096

// Settings is configuration only; runtime and fixture transport options are separate.
type Settings struct {
	Enabled      bool          `yaml:"enabled" json:"enabled"`
	Interface    string        `yaml:"interface" json:"interface"`
	ServerIP     string        `yaml:"server_ip" json:"server_ip"`
	Subnet       string        `yaml:"subnet" json:"subnet"`
	Gateway      string        `yaml:"gateway" json:"gateway"`
	RangeStart   string        `yaml:"range_start" json:"range_start"`
	RangeEnd     string        `yaml:"range_end" json:"range_end"`
	LeaseSeconds int           `yaml:"lease_seconds" json:"lease_seconds"`
	LocalDomain  string        `yaml:"local_domain" json:"local_domain"`
	MaxLeases    int           `yaml:"max_leases" json:"max_leases"`
	Reservations []Reservation `yaml:"reservations,omitempty" json:"reservations"`
}

type Reservation struct {
	ID       string `yaml:"id" json:"id"`
	MAC      string `yaml:"mac,omitempty" json:"mac,omitempty"`
	ClientID string `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	Address  string `yaml:"address" json:"address"`
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`
}

func (s Settings) Capacity() int {
	if s.MaxLeases == 0 {
		return DefaultMaxLeases
	}
	return s.MaxLeases
}
func (s Settings) Clone() Settings {
	s.Reservations = append([]Reservation(nil), s.Reservations...)
	return s
}

func validLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
func validDomain(s string) bool {
	// Reserve a full 63-byte hostname label plus its dot within the 253-byte
	// presentation limit; PTR RDATA must obey the same DNS name bound.
	if len(s) > 189 || strings.EqualFold(s, "local") || strings.HasSuffix(strings.ToLower(s), ".local") {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if !validLabel(l) {
			return false
		}
	}
	return true
}
func ipv4Number(a netip.Addr) uint32 { b := a.As4(); return binary.BigEndian.Uint32(b[:]) }
func numberIPv4(n uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	return netip.AddrFrom4(b)
}
func usable(a netip.Addr, p netip.Prefix) bool {
	if !a.Is4() || !p.IsValid() || !p.Addr().Is4() || !p.Contains(a) || a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.As4()[0] >= 240 {
		return false
	}
	n := ipv4Number(a)
	base := ipv4Number(p.Masked().Addr())
	end := uint64(base) + (uint64(1) << uint(32-p.Bits())) - 1
	return n != base && uint64(n) != end
}
func reservationKey(r Reservation) (string, error) {
	if (r.MAC == "") == (r.ClientID == "") {
		return "", fmt.Errorf("exactly one of mac and client_id is required")
	}
	if r.ClientID != "" {
		b, err := hex.DecodeString(r.ClientID)
		if err != nil || len(b) == 0 || len(b) > 255 {
			return "", fmt.Errorf("client_id must be 1..255 hex-encoded bytes")
		}
		return "id:" + string(b), nil
	}
	m, err := net.ParseMAC(r.MAC)
	if err != nil || len(m) != 6 || m[0]&1 != 0 || string(m) == string(make([]byte, 6)) {
		return "", fmt.Errorf("mac must be a unicast Ethernet address")
	}
	return "mac:" + string(m), nil
}

// ValidateSettings allows incomplete disabled configuration, but validates every
// supplied value. Mandatory fields and cross-field checks are required to enable.
func ValidateSettings(s Settings) error {
	fail := func(field, msg string) error { return fmt.Errorf("dhcp.%s: %s", field, msg) }
	if s.Capacity() < 1 || s.Capacity() > MaximumLeases {
		return fail("max_leases", "must be 1..4096")
	}
	if len(s.Reservations) > s.Capacity() {
		return fail("reservations", "exceeds lease capacity")
	}
	if (s.Enabled || s.Interface != "") && (len(s.Interface) == 0 || len(s.Interface) > 15 || strings.ContainsAny(s.Interface, " /\t\r\n\x00")) {
		return fail("interface", "expected interface name (1..15 bytes)")
	}
	if (s.Enabled || s.LeaseSeconds != 0) && (s.LeaseSeconds < 60 || s.LeaseSeconds > 604800) {
		return fail("lease_seconds", "must be 60..604800")
	}
	if (s.Enabled || s.LocalDomain != "") && !validDomain(s.LocalDomain) {
		return fail("local_domain", "expected DNS domain outside .local, at most 189 bytes")
	}
	var p netip.Prefix
	if s.Enabled || s.Subnet != "" {
		var err error
		p, err = netip.ParsePrefix(s.Subnet)
		if err != nil || !p.Addr().Is4() || p.Bits() > 30 || p != p.Masked() {
			return fail("subnet", "expected canonical IPv4 subnet /0../30")
		}
	}
	addresses := make(map[string]netip.Addr)
	for _, v := range []struct{ key, value string }{{"server_ip", s.ServerIP}, {"gateway", s.Gateway}, {"range_start", s.RangeStart}, {"range_end", s.RangeEnd}} {
		if !s.Enabled && v.value == "" {
			continue
		}
		a, err := netip.ParseAddr(v.value)
		if err != nil || !a.Is4() || a.IsUnspecified() || a.IsMulticast() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.As4()[0] >= 240 || (p.IsValid() && !usable(a, p)) {
			return fail(v.key, "expected usable IPv4 address in subnet")
		}
		addresses[v.key] = a
	}
	start, end := addresses["range_start"], addresses["range_end"]
	if start.IsValid() && end.IsValid() {
		lo, hi := uint64(ipv4Number(start)), uint64(ipv4Number(end))
		if hi < lo || hi-lo+1 > 65536 {
			return fail("range_end", "pool must contain 1..65536 addresses")
		}
		for _, key := range []string{"server_ip", "gateway"} {
			a := addresses[key]
			if a.IsValid() && a.Compare(start) >= 0 && a.Compare(end) <= 0 {
				return fail(key, "must be outside dynamic pool")
			}
		}
	}
	if addresses["server_ip"].IsValid() && addresses["server_ip"] == addresses["gateway"] {
		return fail("gateway", "must differ from server_ip")
	}
	ids, keys, ips, names := map[string]bool{}, map[string]bool{}, map[netip.Addr]bool{}, map[string]bool{}
	for i, r := range s.Reservations {
		field := fmt.Sprintf("reservations[%d]", i)
		if !validLabel(r.ID) || ids[r.ID] {
			return fail(field, "invalid or duplicate id")
		}
		ids[r.ID] = true
		key, err := reservationKey(r)
		if err != nil {
			return fail(field, err.Error())
		}
		if keys[key] {
			return fail(field, "duplicate identity")
		}
		keys[key] = true
		a, err := netip.ParseAddr(r.Address)
		if err != nil || !a.Is4() || !p.IsValid() || !usable(a, p) || a == addresses["server_ip"] || a == addresses["gateway"] || ips[a] {
			return fail(field, "invalid or duplicate address")
		}
		ips[a] = true
		if r.Hostname != "" {
			name := strings.ToLower(r.Hostname)
			if !validLabel(name) || names[name] {
				return fail(field, "invalid or duplicate hostname")
			}
			names[name] = true
		}
	}
	return nil
}

// ValidateTransition is also used by the runtime at its owner boundary.
func ValidateTransition(old, next Settings) error {
	if err := ValidateSettings(next); err != nil {
		return err
	}
	if old.Enabled && next.Enabled && (old.Interface != next.Interface || old.ServerIP != next.ServerIP || old.Subnet != next.Subnet || old.LocalDomain != next.LocalDomain) {
		return fmt.Errorf("dhcp: disable before changing interface, server_ip, subnet or local_domain")
	}
	return nil
}

// ValidateDNS accepts configured wildcard listeners which may be dual-stack.
// Runtime must use ValidateBoundDNS with socket-verified IPv4 endpoints.
func ValidateDNS(s Settings, listeners []string) error {
	return validateDNS(s, listeners, true)
}

// ValidateBoundDNS requires actual IPv4 capability, never an IPv6 address alone.
// The listener owner represents verified dual-stack sockets as 0.0.0.0:port.
func ValidateBoundDNS(s Settings, listeners []string) error {
	return validateDNS(s, listeners, false)
}

func validateDNS(s Settings, listeners []string, allowDualStack bool) error {
	if !s.Enabled {
		return nil
	}
	server, _ := netip.ParseAddr(s.ServerIP)
	for _, v := range listeners {
		a, err := netip.ParseAddrPort(v)
		if err == nil && a.Port() == 53 && (a.Addr() == server || a.Addr() == netip.IPv4Unspecified() || (allowDualStack && a.Addr() == netip.IPv6Unspecified())) {
			return nil
		}
	}
	if allowDualStack {
		return fmt.Errorf("dhcp: DNS must listen on server_ip:53, 0.0.0.0:53 or dual-stack [::]:53")
	}
	return fmt.Errorf("dhcp: DNS needs IPv4-capable UDP and TCP listeners on server_ip:53 or all addresses")
}
