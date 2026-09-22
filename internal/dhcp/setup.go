package dhcp

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Setup is a read-only proposal. Config remains authoritative until the caller
// explicitly saves edits with its original configuration revision.
type Setup struct {
	Config       Settings `json:"config"`
	Suggested    []string `json:"suggested"`
	FixedAddress string   `json:"fixed_address"`
	Message      string   `json:"message"`
}

type setupAddress struct {
	Interface     string
	Prefix        netip.Prefix
	Fixed         string
	SkipSelection bool
}

// Retain every local IPv4 address for pool exclusion, even when its interface
// cannot be selected as the DHCP LAN (for example a loopback service address).
func setupAddresses(iface net.Interface, list []net.Addr, savedInterface string) []setupAddress {
	skip := iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) != 6 || iface.MTU < 576
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "podman", "tun", "tap", "utun", "tailscale", "wg"} {
		if strings.HasPrefix(iface.Name, prefix) && savedInterface != iface.Name {
			skip = true
		}
	}
	var result []setupAddress
	for _, address := range list {
		p, err := netip.ParsePrefix(address.String())
		if err == nil && p.Addr().Is4() {
			result = append(result, setupAddress{Interface: iface.Name, Prefix: p, Fixed: "unknown", SkipSelection: skip})
		}
	}
	return result
}

type setupRoute struct {
	Interface string
	Gateway   netip.Addr
	Metric    uint64
}

func suggestSetup(saved Settings, addresses []setupAddress, routes []setupRoute) Setup {
	v := Setup{Config: saved.Clone(), Suggested: []string{}, FixedAddress: "unknown"}
	if v.Config.Reservations == nil {
		v.Config.Reservations = []Reservation{}
	}
	set := func(key string, target *string, value string) {
		if *target == "" && value != "" {
			*target = value
			v.Suggested = append(v.Suggested, key)
		}
	}
	set("local_domain", &v.Config.LocalDomain, "home.arpa")
	if v.Config.LeaseSeconds == 0 {
		v.Config.LeaseSeconds = 86400
		v.Suggested = append(v.Suggested, "lease_seconds")
	}
	// Respect saved topology. Do not merge a detected network into a different
	// manually configured one. Equal-cost routes or address aliases need a choice.
	var chosen setupAddress
	var gateway netip.Addr
	best, matches := ^uint64(0), 0
	for _, a := range addresses {
		if a.SkipSelection || !a.Prefix.IsValid() || !a.Prefix.Addr().Is4() || a.Prefix.Bits() > 30 || !usable(a.Prefix.Addr(), a.Prefix) {
			continue
		}
		if (saved.Interface != "" && saved.Interface != a.Interface) ||
			(saved.ServerIP != "" && saved.ServerIP != a.Prefix.Addr().String()) ||
			(saved.Subnet != "" && saved.Subnet != a.Prefix.Masked().String()) {
			continue
		}
		for _, r := range routes {
			if r.Interface != a.Interface || !usable(r.Gateway, a.Prefix) || r.Gateway == a.Prefix.Addr() {
				continue
			}
			candidate := saved
			candidate.Interface, candidate.ServerIP, candidate.Subnet = a.Interface, a.Prefix.Addr().String(), a.Prefix.Masked().String()
			if candidate.Gateway == "" {
				candidate.Gateway = r.Gateway.String()
			}
			if ValidateSettings(candidate) != nil {
				continue
			}
			if r.Metric < best {
				best, matches = r.Metric, 0
			}
			if r.Metric == best {
				// Duplicate kernel routes are not competing networks.
				if matches == 1 && chosen == a && gateway == r.Gateway {
					continue
				}
				chosen, gateway = a, r.Gateway
				matches++
			}
		}
	}
	if matches == 1 {
		set("interface", &v.Config.Interface, chosen.Interface)
		set("server_ip", &v.Config.ServerIP, chosen.Prefix.Addr().String())
		set("subnet", &v.Config.Subnet, chosen.Prefix.Masked().String())
		set("gateway", &v.Config.Gateway, gateway.String())
		if chosen.Fixed == "yes" || chosen.Fixed == "no" {
			v.FixedAddress = chosen.Fixed
		}
	} else {
		v.Message = "Couldn’t choose one network automatically. Open Edit settings to enter your network details."
	}
	if v.Config.RangeStart == "" && v.Config.RangeEnd == "" {
		start, end := setupPool(v.Config, addresses)
		set("range_start", &v.Config.RangeStart, start)
		set("range_end", &v.Config.RangeEnd, end)
		if start == "" && matches == 1 {
			v.Message = "Couldn’t suggest an address range. Open Edit settings to choose one."
		}
	}
	return v
}

// Prefer the upper half of the subnet, at most 100 addresses. Exclude known
// local and reserved addresses; this does not claim other devices are unused.
// Work is bounded even for very large subnets, and /30 exhaustion is explicit.
func setupPool(s Settings, addresses []setupAddress) (string, string) {
	p, err := netip.ParsePrefix(s.Subnet)
	server, se := netip.ParseAddr(s.ServerIP)
	gateway, ge := netip.ParseAddr(s.Gateway)
	if err != nil || !p.Addr().Is4() || p.Bits() > 30 || p != p.Masked() || se != nil || ge != nil || !usable(server, p) || !usable(gateway, p) || server == gateway {
		return "", ""
	}
	excluded := map[netip.Addr]bool{server: true, gateway: true}
	for _, a := range addresses {
		excluded[a.Prefix.Addr()] = true
	}
	for _, r := range s.Reservations {
		if a, err := netip.ParseAddr(r.Address); err == nil {
			excluded[a] = true
		}
	}
	base := uint64(ipv4Number(p.Addr()))
	size := uint64(1) << uint(32-p.Bits())
	var bestStart, bestSize uint64
	for _, interval := range [][2]uint64{{base + size/2, base + size - 1}, {base + 1, base + size/2}} {
		var runStart, runSize uint64
		for n := interval[0]; n < interval[1] && n-interval[0] < 65536; n++ {
			a := numberIPv4(uint32(n))
			if excluded[a] || !usable(a, p) {
				runSize = 0
				continue
			}
			if runSize == 0 {
				runStart = n
			}
			runSize++
			if runSize > bestSize {
				bestStart, bestSize = runStart, runSize
			}
			if bestSize == 100 {
				break
			}
		}
		if bestSize != 0 {
			break
		}
	}
	if bestSize == 0 {
		return "", ""
	}
	return numberIPv4(uint32(bestStart)).String(), numberIPv4(uint32(bestStart + bestSize - 1)).String()
}

// Linux's main IPv4 route table. Ignore down, reject, non-default and direct
// routes. Policy-routing-only hosts fall back to manual configuration.
func parseSetupRoutes(r io.Reader) []setupRoute {
	var routes []setupRoute
	scanner := bufio.NewScanner(io.LimitReader(r, 1<<20))
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, fe := strconv.ParseUint(f[3], 16, 32)
		gateway, ge := strconv.ParseUint(f[2], 16, 32)
		metric, me := strconv.ParseUint(f[6], 10, 64)
		if fe != nil || ge != nil || me != nil || flags&3 != 3 || flags&0x200 != 0 || gateway == 0 {
			continue
		}
		var b [4]byte
		binary.NativeEndian.PutUint32(b[:], uint32(gateway))
		routes = append(routes, setupRoute{Interface: f[0], Gateway: netip.AddrFrom4(b), Metric: metric})
	}
	return routes
}
