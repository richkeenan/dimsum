package lists

import (
	"net/netip"
	"strings"

	"github.com/richkeenan/dimsum/internal/policy"
)

func parseHosts(text string) ([]policy.Rule, string) {
	text, _, _ = strings.Cut(text, "#")
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil, ""
	}
	if len(fields) < 2 {
		return nil, "invalid-hosts"
	}
	addr, err := netip.ParseAddr(fields[0])
	if err != nil {
		return nil, "invalid-address"
	}
	if len(fields) == 2 {
		switch fields[0] + " " + fields[1] {
		case "255.255.255.255 broadcasthost", "fe80::1%lo0 localhost", "ff00::0 ip6-localnet", "ff00::0 ip6-mcastprefix", "ff02::3 ip6-allhosts", "0.0.0.0 0.0.0.0":
			return nil, ""
		}
	}
	// These conventional IPv6 boilerplate mappings are not filtering rules.
	if (fields[0] == "ff02::1" && len(fields) == 2 && fields[1] == "ip6-allnodes") || (fields[0] == "ff02::2" && len(fields) == 2 && fields[1] == "ip6-allrouters") {
		return nil, ""
	}
	if !addr.IsUnspecified() && !addr.IsLoopback() {
		return nil, "non-sinkhole-mapping"
	}
	var rules []policy.Rule
	for _, field := range fields[1:] {
		rule, code := domainRule(field, policy.Exact)
		if code != "" {
			return nil, code
		}
		switch rule.Pattern {
		case "localhost", "localhost.localdomain", "local", "broadcasthost", "ip6-localhost", "ip6-loopback", "ip6-localnet", "ip6-mcastprefix":
			continue
		}
		rules = append(rules, rule)
	}
	return rules, ""
}
