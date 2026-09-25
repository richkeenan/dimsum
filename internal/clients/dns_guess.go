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

// DNSGuessSelectors returns exact names and broad suffixes for storage to select.
// The broader AWS suffix is narrowed again by dnsGuessRule before attribution.
func DNSGuessSelectors() (exact, suffix []string) {
	return dnsGuessRules.selectors()
}

func (rules guessRules) selectors() (exact, suffix []string) {
	for _, r := range rules {
		exact = append(exact, r.Domains...)
	}
	return exact, []string{"amazonaws.com"}
}

func dnsGuessRule(domain string) int {
	return dnsGuessRules.match(domain)
}

func (rules guessRules) match(domain string) int {
	for i, r := range rules {
		if slices.Contains(r.Domains, domain) {
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
			return awsIoTRule
		}
	}
	return -1
}

func dnsGuess(address netip.Addr, rows []DNSActivity, now time.Time) Name {
	return dnsGuessRules.guess(address, rows, now)
}

func (rules guessRules) guess(address netip.Addr, rows []DNSActivity, now time.Time) Name {
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
		if rule := rules.match(r.Domain); rule != -1 {
			groups[rule] = append(groups[rule], r)
		}
	}
	// A more specific product family beats generic cloud infrastructure. Any
	// competing specific family suppresses attribution, even before corroboration.
	selected := -1
	for rule := range groups {
		if rule == awsIoTRule {
			continue
		}
		if selected >= 0 {
			return Name{}
		}
		selected = rule
	}
	if selected < 0 {
		selected = awsIoTRule
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
	rule := awsIoTGuess
	if selected != awsIoTRule {
		rule = rules[selected]
	}
	slices.SortFunc(matched, func(a, b DNSActivity) int { return strings.Compare(a.Domain, b.Domain) })
	guess := &DNSGuess{Rule: rule.ID, Reason: rule.Reason, Domains: []DNSGuessDomain{}}
	var updated time.Time
	for _, r := range matched {
		guess.Domains = append(guess.Domains, DNSGuessDomain{r.Domain, r.First, r.Last, r.Count})
		if r.Last.After(updated) {
			updated = r.Last
		}
	}
	guess.Expires = support.Add(DNSGuessLifetime)
	return Name{Address: address, Name: rule.Name, Source: "dns-guess", Fresh: true, Updated: updated, Expires: guess.Expires, Device: &Enrichment{Icon: rule.Icon, Category: rule.Category, Reason: rule.Reason, Inferred: true, Fresh: true, Evidence: []Evidence{}, DNSGuess: guess}}
}

func applyDNSGuess(n Name, address netip.Addr, rows []DNSActivity, now time.Time) Name {
	return dnsGuessRules.apply(n, address, rows, now)
}

func (rules guessRules) apply(n Name, address netip.Addr, rows []DNSActivity, now time.Time) Name {
	guess := rules.guess(address, rows, now)
	if guess.Name == "" {
		return n
	}
	// Name precedence and device evidence are independent. DHCP/configured
	// names must not discard eligible history evidence, nor may inferred history
	// replace a category/model already supplied by stronger discovery.
	device := cloneDevice(n.Device)
	if n.Name == "" {
		n = guess
	}
	if device == nil {
		device = guess.Device
	} else {
		device.DNSGuess = guess.Device.DNSGuess
		if device.Category == "" || device.Category == "unknown" {
			device.Icon = guess.Device.Icon
			device.Category = guess.Device.Category
			device.Reason = guess.Device.Reason
			device.Inferred = guess.Device.Inferred
			device.Fresh = guess.Device.Fresh
		}
	}
	n.Device = device
	return n
}

// ReplaceDNSActivity atomically publishes a private snapshot. Oversized clients
// are omitted rather than arbitrarily truncating away a conflicting signal.
func (m *Manager) ReplaceDNSActivity(rows []DNSActivity) {
	m.ReplaceDNSActivityForView(m.current(), rows)
}

// DNSGuessSelectors captures the immutable catalogue used for a history refresh.
func (m *Manager) DNSGuessSelectors() (*View, []string, []string) {
	v := m.current()
	rules, _ := catalogueFor(v)
	exact, suffix := rules.selectors()
	return v, exact, suffix
}

func (m *Manager) ReplaceDNSActivityForView(view *View, rows []DNSActivity) {
	rules, scope := catalogueFor(view)
	_, currentScope := catalogueFor(m.current())
	if scope != currentScope {
		return
	}
	byAddress := map[netip.Addr][]DNSActivity{}
	overflow := map[netip.Addr]bool{}
	for _, r := range rows {
		a := r.Address.Unmap()
		if !a.IsValid() || a.IsUnspecified() || a.IsMulticast() || a.IsLoopback() || rules.match(r.Domain) == -1 || overflow[a] {
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
	m.dnsScope = scope
	m.mu.Unlock()
}
