package clients

import (
	"net/netip"
	"slices"
	"strings"
	"time"
)

const DNSGuessLifetime = 24 * time.Hour

// DNSActivity is a bounded aggregate of actual retained DNS questions, not
// upstream aliases or a claim that a physical product owns the client address.
type DNSActivity struct {
	Corroborated time.Time // Most recent query at least 30 seconds before Last.
	Address      netip.Addr
	Domain       string
	First, Last  time.Time
	Count        uint64
}
type DNSGuessDomain struct {
	Domain    string    `json:"domain"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Queries   uint64    `json:"queries,string"`
}
type DNSGuess struct {
	Rule    string           `json:"rule"`
	Reason  string           `json:"reason"`
	Domains []DNSGuessDomain `json:"domains"`
	Expires time.Time        `json:"expires"`
}
type guessRule struct {
	id, name, category, reason string
	domains                    []string
}

// Deliberately excludes websites, login services, NTP and shared cloud CDNs.
// Nintendo platform-specific service names are documented by Pretendo's server
// catalogue. These remain guesses: apps and emulators can use the same services.
var dnsGuessRules = []guessRule{
	{"ring", "Ring device", "camera", "Queries to Ring firmware services", []string{"fw-eventstream.ring.com", "fw-snaps.prod.gws.ring.amazon.dev"}},
	{"switch", "Nintendo Switch", "console", "Queries to Nintendo Switch system services", []string{"sun.hac.lp1.d4c.nintendo.net", "atumn.hac.lp1.d4c.nintendo.net", "aqua.hac.lp1.d4c.nintendo.net"}},
	{"switch2", "Nintendo Switch 2", "console", "Queries to Nintendo Switch 2 system services", []string{"sun.p01.lp1.d4c.srv.nintendo.net", "atumn.p01.lp1.d4c.srv.nintendo.net", "aqua.p01.lp1.d4c.srv.nintendo.net"}},
	{"wiiu", "Nintendo Wii U", "console", "Queries to Nintendo Wii U system services", []string{"nus.wup.shop.nintendo.net", "tagaya.wup.shop.nintendo.net"}},
	{"aws-iot", "IoT device", "unknown", "Queries to an AWS IoT device endpoint; manufacturer unknown", nil},
}

// DNSGuessSelectors returns exact names and broad suffixes for storage to select.
// The broader AWS suffix is narrowed again by dnsGuessRule before attribution.
func DNSGuessSelectors() (exact, suffix []string) {
	for _, r := range dnsGuessRules {
		exact = append(exact, r.domains...)
	}
	return exact, []string{"amazonaws.com"}
}

func dnsGuessRule(domain string) int {
	for i, r := range dnsGuessRules {
		if slices.Contains(r.domains, domain) {
			return i
		}
	}
	p := strings.Split(domain, ".")
	if len(p) == 5 && p[1] == "iot" && p[3] == "amazonaws" && p[4] == "com" {
		id, ok := strings.CutSuffix(p[0], "-ats")
		region := strings.Split(p[2], "-")
		if ok && id != "" && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789") == "" && len(region) >= 3 && len(region[0]) == 2 && len(region[len(region)-1]) == 1 && strings.Trim(region[len(region)-1], "0123456789") == "" {
			for _, part := range region[:len(region)-1] {
				if part == "" || strings.Trim(part, "abcdefghijklmnopqrstuvwxyz") != "" {
					return -1
				}
			}
			return len(dnsGuessRules) - 1
		}
	}
	return -1
}

func dnsGuess(address netip.Addr, rows []DNSActivity, now time.Time) Name {
	groups := make(map[int][]DNSActivity)
	seen := make(map[string]bool)
	for _, r := range rows {
		if r.Count == 0 || r.First.After(r.Last) || r.Last.After(now) || !now.Before(r.Last.Add(DNSGuessLifetime)) {
			continue
		}
		if seen[r.Domain] {
			continue
		}
		seen[r.Domain] = true
		if rule := dnsGuessRule(r.Domain); rule >= 0 {
			groups[rule] = append(groups[rule], r)
		}
	}
	// A more specific product family beats generic cloud infrastructure. Any
	// competing specific family suppresses attribution, even before corroboration.
	selected := -1
	for rule := range groups {
		if rule == len(dnsGuessRules)-1 {
			continue
		}
		if selected >= 0 {
			return Name{}
		}
		selected = rule
	}
	if selected < 0 {
		selected = len(dnsGuessRules) - 1
	}
	matched := groups[selected]
	var support time.Time
	for i, a := range matched {
		for _, b := range matched[i+1:] {
			pair := a.Last
			if b.Last.Before(pair) {
				pair = b.Last
			}
			if pair.After(support) {
				support = pair
			}
		}
	}
	for _, r := range matched {
		pair := r.Corroborated
		if pair.IsZero() {
			pair = r.First
		}
		if r.Count >= 2 && r.Last.Sub(pair) >= 30*time.Second && pair.After(support) {
			support = pair
		}
	}
	if support.IsZero() || !now.Before(support.Add(DNSGuessLifetime)) {
		return Name{}
	}
	rule := dnsGuessRules[selected]
	slices.SortFunc(matched, func(a, b DNSActivity) int { return strings.Compare(a.Domain, b.Domain) })
	guess := &DNSGuess{Rule: rule.id, Reason: rule.reason, Domains: []DNSGuessDomain{}}
	var updated time.Time
	for _, r := range matched {
		guess.Domains = append(guess.Domains, DNSGuessDomain{r.Domain, r.First, r.Last, r.Count})
		if r.Last.After(updated) {
			updated = r.Last
		}
	}
	guess.Expires = support.Add(DNSGuessLifetime)
	return Name{Address: address, Name: rule.name, Source: "dns-guess", Fresh: true, Updated: updated, Expires: guess.Expires, Device: &Enrichment{Category: rule.category, Reason: rule.reason, Inferred: true, Fresh: true, Evidence: []Evidence{}, DNSGuess: guess}}
}

func applyDNSGuess(n Name, address netip.Addr, rows []DNSActivity, now time.Time) Name {
	if n.Name != "" {
		return n
	}
	if guess := dnsGuess(address, rows, now); guess.Name != "" {
		return guess
	}
	return n
}

// ReplaceDNSActivity atomically publishes a private snapshot. Oversized clients
// are omitted rather than arbitrarily truncating away a conflicting signal.
func (m *Manager) ReplaceDNSActivity(rows []DNSActivity) {
	byAddress := map[netip.Addr][]DNSActivity{}
	overflow := map[netip.Addr]bool{}
	for _, r := range rows {
		a := r.Address.Unmap()
		if !a.IsValid() || a.IsUnspecified() || a.IsMulticast() || a.IsLoopback() || dnsGuessRule(r.Domain) < 0 || overflow[a] {
			continue
		}
		if len(byAddress) >= 4096 && byAddress[a] == nil {
			continue
		}
		if len(byAddress[a]) >= 16 {
			delete(byAddress, a)
			overflow[a] = true
			continue
		}
		byAddress[a] = append(byAddress[a], r)
	}
	m.mu.Lock()
	m.dnsActivity = byAddress
	m.mu.Unlock()
}
