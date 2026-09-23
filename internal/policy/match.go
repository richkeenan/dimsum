package policy

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// Matcher is an immutable generation safe for concurrent readers. It scans all
// rules intentionally: this is the correctness oracle for later compact indexes.
// In particular no approximate prefilter can drop an allow exception.
type Matcher struct {
	generation                                             uint64
	rules                                                  []compiledRule
	regexBytes                                             int64 // validated aggregate charge, reusable by snapshot compiler
	regexCount, maxExpressionBytes, maxProgramInstructions int
}

// Rule returns a value copy of the original diagnostic input, not compiled
// state. Mutating it cannot affect this or another generation.
func (m *Matcher) Rule(id string) (Rule, bool) {
	for _, r := range m.rules {
		if r.rule.ID == id {
			return r.rule, true
		}
	}
	return Rule{}, false
}

type compiledRule struct {
	rule        Rule
	kind        Kind
	name        Name
	labels      []string
	descendants bool
	re          *regexp.Regexp
}

// Compile validates the entire enabled rule set before returning a new matcher.
// It never mutates the inputs or a previously returned generation. Go is the
// only supported regex dialect (empty or "go"). SourceText is retained verbatim.
func Compile(generation uint64, rules []Rule, limits Limits) (*Matcher, error) {
	if limits.MaxRegex <= 0 || limits.MaxExpressionBytes <= 0 || limits.MaxProgramInstructions <= 0 || limits.MaxTotalRegexBytes <= 0 {
		return nil, fmt.Errorf("policy: all regex budgets must be positive")
	}
	m := &Matcher{generation: generation}
	seen := make(map[string]bool)
	count := 0
	var total int64
	for _, r := range rules {
		if err := validateRuleScope(r); err != nil {
			return nil, fmt.Errorf("policy: rule %q: %w", r.ID, err)
		}
		if r.ID == "" || seen[r.ID] {
			return nil, fmt.Errorf("policy: empty or duplicate rule ID %q", r.ID)
		}
		seen[r.ID] = true
		if rank(r.Class) < 0 {
			return nil, fmt.Errorf("policy: rule %q: unsupported class %q", r.ID, r.Class)
		}
		c := compiledRule{rule: r, kind: r.Kind}
		var err error
		if r.Kind != Regex && r.Dialect != "" {
			return nil, fmt.Errorf("policy: rule %q: dialect only applies to regex", r.ID)
		}
		switch r.Kind {
		case Exact, Suffix:
			c.name, err = NormalizeName(r.Pattern)
			c.labels = c.name.labels()
		case Wildcard:
			if !strings.HasPrefix(r.Pattern, "*.") {
				err = fmt.Errorf("wildcard must begin with *.")
			} else {
				c.name, err = NormalizeName(r.Pattern[2:])
				c.labels = c.name.labels()
			}
		case Glob:
			c.labels, c.descendants, err = normalizeGlob(r.Pattern)
		case Regex:
			count++
			if count > limits.MaxRegex {
				err = fmt.Errorf("regex count budget exceeded")
				break
			}
			var charge int64
			var instructions int
			c.re, charge, instructions, err = compileRegex(r.Pattern, r.Dialect, limits, limits.MaxTotalRegexBytes-total)
			m.maxExpressionBytes = max(m.maxExpressionBytes, len(r.Pattern))
			m.maxProgramInstructions = max(m.maxProgramInstructions, instructions)
			total += charge
		default:
			err = fmt.Errorf("unsupported rule form %q", r.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("policy: rule %q: %w", r.ID, err)
		}
		m.rules = append(m.rules, c)
	}
	m.regexBytes = total
	m.regexCount = count
	return m, nil
}

func normalizeGlob(s string) ([]string, bool, error) {
	// Map IDNA label separators before interpreting glob boundaries. Removing
	// the root dot per literal label could otherwise hide an empty interior label.
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\u3002', '\uff0e', '\uff61':
			return '.'
		default:
			return r
		}
	}, s)
	s = strings.TrimSuffix(s, ".")
	descendants := strings.HasPrefix(s, "*.")
	if descendants {
		s = s[2:]
	}
	labels := strings.Split(s, ".")
	length := 1
	for i, label := range labels {
		if label == "" {
			return nil, false, fmt.Errorf("empty glob label")
		}
		if strings.ContainsAny(label, "*?") {
			// Wildcards inside IDNs cannot be punycode-normalized meaningfully.
			// Literal IDN labels elsewhere in the same pattern are supported.
			if len(label) > 63 || strings.Contains(label, "**") {
				return nil, false, fmt.Errorf("invalid glob label")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '*' || c == '?') {
					return nil, false, fmt.Errorf("unsupported glob syntax")
				}
			}
			labels[i] = strings.ToLower(label)
		} else {
			n, err := NormalizeName(label)
			if err != nil || len(n.labels()) != 1 {
				return nil, false, fmt.Errorf("invalid literal glob label %q", label)
			}
			labels[i] = n.labels()[0]
		}
		length += 1 + len(labels[i])
	}
	if descendants {
		length += 2
	}
	if length > 255 {
		return nil, false, fmt.Errorf("glob name too long")
	}
	return labels, descendants, nil
}

func compileRegex(expr, dialect string, limits Limits, remaining int64) (*regexp.Regexp, int64, int, error) {
	if dialect != "" && dialect != "go" {
		return nil, 0, 0, fmt.Errorf("unsupported regex dialect %q", dialect)
	}
	if len(expr) > limits.MaxExpressionBytes {
		return nil, 0, 0, fmt.Errorf("regex expression byte budget exceeded")
	}
	tree, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return nil, 0, 0, err
	}
	// Bound repetition expansion before Simplify allocates the expanded tree.
	// Capped arithmetic also bounds hostile nested counted repetitions.
	cap := int64(limits.MaxProgramInstructions)
	cost := expandedCost(tree, cap, false)
	if cost > cap-2 {
		return nil, 0, 0, fmt.Errorf("regex program instruction budget exceeded")
	}
	// Conservative accounting units for AST/program/runes and regexp overhead,
	// not a claim about exact Go heap/RSS. Benchmark task must calibrate defaults.
	base := int64(4096) + int64(len(expr))*8
	if remaining < base+512 {
		return nil, 0, 0, fmt.Errorf("total regex memory budget exceeded")
	}
	memoryCap := (remaining - base) / 256
	memoryCost := expandedCost(tree, memoryCap, true)
	if memoryCost > memoryCap-2 {
		return nil, 0, 0, fmt.Errorf("total regex memory budget exceeded")
	}
	charge := base + (memoryCost+2)*256
	prog, err := syntax.Compile(tree.Simplify())
	if err != nil {
		return nil, 0, 0, err
	}
	if len(prog.Inst) > limits.MaxProgramInstructions {
		return nil, 0, 0, fmt.Errorf("regex program instruction budget exceeded")
	}
	re, err := regexp.Compile(expr)
	// Retain the admission threshold, including the pre-expansion bound, so a
	// shared snapshot can be checked against tighter limits without reparsing.
	return re, charge, max(int(cost+2), len(prog.Inst)), err
}

// expandedCost is a conservative expanded instruction bound, optionally adding
// memory units for character-class rune tables (16 bytes/rune, rounded up).
// It saturates at cap before any unbounded multiplication or expansion.
func expandedCost(r *syntax.Regexp, cap int64, memory bool) int64 {
	add := func(a, b int64) int64 {
		if a >= cap-b {
			return cap
		}
		return a + b
	}
	mul := func(a, b int64) int64 {
		if b != 0 && a >= cap/b {
			return cap
		}
		return a * b
	}
	switch r.Op {
	case syntax.OpLiteral:
		return min(int64(len(r.Rune)), cap)
	case syntax.OpCharClass:
		if memory {
			return min(1+(int64(len(r.Rune))+15)/16, cap)
		}
		return 1
	case syntax.OpConcat, syntax.OpAlternate:
		var n int64
		for _, sub := range r.Sub {
			n = add(n, expandedCost(sub, cap, memory))
		}
		if r.Op == syntax.OpAlternate {
			n = add(n, int64(len(r.Sub)-1))
		}
		return n
	case syntax.OpCapture:
		return add(2, expandedCost(r.Sub[0], cap, memory))
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
		return add(2, expandedCost(r.Sub[0], cap, memory))
	case syntax.OpRepeat:
		copies := r.Max
		if copies < 0 {
			copies = r.Min + 1
		}
		return add(1, mul(add(2, expandedCost(r.Sub[0], cap, memory)), int64(copies)))
	default:
		return 1
	}
}

func (r *compiledRule) matches(n Name, labels []string, display string) bool {
	switch r.kind {
	case Exact:
		return n == r.name
	case Suffix, Wildcard:
		if len(labels) < len(r.labels) || r.kind == Wildcard && len(labels) == len(r.labels) {
			return false
		}
		for i, label := range r.labels {
			if labels[len(labels)-len(r.labels)+i] != label {
				return false
			}
		}
		return true
	case Glob:
		if r.descendants {
			if len(labels) <= len(r.labels) {
				return false
			}
			labels = labels[len(labels)-len(r.labels):]
		} else if len(labels) != len(r.labels) {
			return false
		}
		for i, p := range r.labels {
			if !labelGlob(p, labels[i]) {
				return false
			}
		}
		return true
	case Regex:
		return r.re.MatchString(display)
	}
	return false
}

// Glob operates on label octets, not display escapes or Unicode runes. The
// bounded dynamic program keeps arbitrary binary labels and '*' unambiguous.
func labelGlob(pattern, label string) bool {
	var prev, next [64]bool
	prev[0] = true
	for i := 0; i < len(pattern); i++ {
		next = [64]bool{}
		if pattern[i] == '*' {
			next[0] = prev[0]
		}
		for j := 1; j <= len(label); j++ {
			switch pattern[i] {
			case '*':
				next[j] = prev[j] || next[j-1]
			case '?':
				next[j] = prev[j-1]
			default:
				next[j] = prev[j-1] && pattern[i] == label[j-1]
			}
		}
		prev = next
	}
	return prev[len(label)]
}
