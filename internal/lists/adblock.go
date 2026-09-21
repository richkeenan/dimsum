package lists

import (
	"strings"

	"github.com/richkeenan/dimsum/internal/policy"
)

func parseAdblock(text string, kind policy.Kind) ([]policy.Rule, string) {
	if text == "[Adblock Plus]" {
		return nil, ""
	}
	if text == "" || strings.HasPrefix(text, "!") || strings.HasPrefix(text, "# ") || text == "#" || text == "[Adblock Plus 2.0]" || text == "[Adblock Plus 1.1]" || text == "[Adblock]" {
		return nil, ""
	}
	if strings.Contains(text, "$") {
		return nil, "unsupported-modifier"
	}
	allow := strings.HasPrefix(text, "@@")
	if allow {
		text = strings.TrimPrefix(text, "@@")
	}
	if strings.HasPrefix(text, "||") && strings.HasSuffix(text, "^") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "||"), "^")
		kind = policy.Suffix
	} else if allow {
		return nil, "unsupported-exception"
	}
	rule, code := domainRule(text, kind)
	if code != "" {
		if allow && code == "invalid-domain" {
			return nil, "invalid-exception"
		}
		return nil, code
	}
	if allow {
		rule.Class = policy.SubscriptionAllow
	}
	return []policy.Rule{rule}, ""
}
