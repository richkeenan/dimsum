package lists

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/richkeenan/dimsum/internal/policy"
)

func domainRule(text string, kind policy.Kind) (policy.Rule, string) {
	if !utf8.ValidString(text) {
		return policy.Rule{}, "invalid-utf8"
	}
	if strings.ContainsAny(text, " /\\\t\r\n$|^@#*?[]!:,") {
		return policy.Rule{}, "unsupported-syntax"
	}
	name, err := policy.NormalizeName(text)
	if err != nil {
		return policy.Rule{}, "invalid-domain"
	}
	normalized := name.Display()
	if _, err := netip.ParseAddr(normalized); err == nil {
		return policy.Rule{}, "ip-literal"
	}
	return policy.Rule{Pattern: normalized, Kind: kind, Class: policy.SubscriptionDeny}, ""
}
func parseDomains(text string, kind policy.Kind) ([]policy.Rule, string) {
	if text == "" || strings.HasPrefix(text, "#") {
		return nil, ""
	}
	rule, code := domainRule(text, kind)
	if code != "" {
		return nil, code
	}
	return []policy.Rule{rule}, ""
}
