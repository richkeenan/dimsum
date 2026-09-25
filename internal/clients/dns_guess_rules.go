package clients

import (
	_ "embed"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type DNSGuessRule struct {
	ID       string   `yaml:"id" json:"id"`
	Name     string   `yaml:"name" json:"name"`
	Category string   `yaml:"category,omitempty" json:"category,omitempty"`
	Reason   string   `yaml:"reason,omitempty" json:"reason,omitempty"`
	Icon     string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Domains  []string `yaml:"domains" json:"domains"`
}
type guessRule = DNSGuessRule
type guessRules []guessRule

//go:embed builtin/dns-guesses.yaml
var builtinDNSGuessYAML string

var dnsGuessRules = func() guessRules {
	rules, err := parseDNSGuessRules(builtinDNSGuessYAML)
	if err != nil {
		panic(fmt.Sprintf("invalid embedded DNS guess catalogue: %v", err))
	}
	return rules
}()

const awsIoTRule = -2

var awsIoTGuess = guessRule{ID: "aws-iot", Name: "IoT device", Category: "unknown", Reason: "Queries to an AWS IoT device endpoint; manufacturer unknown"}

func parseDNSGuessRules(source string) (guessRules, error) {
	decoder := yaml.NewDecoder(strings.NewReader(source))
	decoder.KnownFields(true)
	var rules guessRules
	if err := decoder.Decode(&rules); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one YAML document")
	}
	return validateGuessRules(rules)
}

func validateGuessRules(rules guessRules) (guessRules, error) {
	ids, domains := map[string]bool{}, map[string]bool{}
	for i := range rules {
		r := &rules[i]
		if !validGuessID(r.ID) || ids[r.ID] {
			return nil, fmt.Errorf("rule %d: invalid or duplicate id %q", i, r.ID)
		}
		ids[r.ID] = true
		if err := normalizeGuessRule(r, false); err != nil {
			return nil, err
		}
		for _, domain := range r.Domains {
			if !validGuessDomain(domain) || domains[domain] {
				return nil, fmt.Errorf("rule %s: invalid or duplicate exact domain %q", r.ID, domain)
			}
			domains[domain] = true
		}
	}
	return rules, nil
}

func validGuessID(id string) bool {
	return len(id) > 0 && len(id) <= 64 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789-_") == "" && id != "aws-iot"
}

// NormalizeDNSGuessRule supplies optional presentation defaults and validates a
// single editor submission. Built-in rules may have every domain excluded.
func NormalizeDNSGuessRule(rule DNSGuessRule, allowEmpty bool) (DNSGuessRule, error) {
	rule = cloneGuessRule(rule)
	if err := normalizeGuessRule(&rule, allowEmpty); err != nil {
		return rule, err
	}
	seen := map[string]bool{}
	for _, domain := range rule.Domains {
		if !validGuessDomain(domain) || seen[domain] {
			return rule, fmt.Errorf("rule %s: invalid or duplicate exact domain %q", rule.ID, domain)
		}
		seen[domain] = true
	}
	return rule, nil
}

func normalizeGuessRule(r *DNSGuessRule, allowEmpty bool) error {
	if strings.ContainsAny(r.Name+r.Reason, "\r\n") {
		return fmt.Errorf("rule %s: name and reason must be single-line text", r.ID)
	}
	if !validGuessID(r.ID) || strings.TrimSpace(r.Name) == "" || len(r.Name) > 256 || !allowEmpty && len(r.Domains) == 0 || len(r.Domains) > 256 {
		return fmt.Errorf("rule %s: valid id, name (up to 256 bytes) and domains (up to 256) required", r.ID)
	}
	if r.Category == "" {
		r.Category = "unknown"
	}
	if !slices.Contains([]string{"unknown", "phone", "tablet", "laptop", "desktop", "tv", "speaker", "printer", "camera", "lighting", "appliance", "server", "console"}, r.Category) {
		return fmt.Errorf("rule %s: invalid category %q", r.ID, r.Category)
	}
	if !ValidIcon(r.Icon) {
		return fmt.Errorf("rule %s: unknown Lucide icon %q", r.ID, r.Icon)
	}
	if len(r.Reason) > 1024 {
		return fmt.Errorf("rule %s: reason exceeds 1024 bytes", r.ID)
	}
	if r.Reason == "" {
		r.Reason = "Inferred from recent DNS queries to recognised device services"
	}
	return nil
}

func validGuessDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' || strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return false
		}
	}
	return true
}
