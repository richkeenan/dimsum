// Package policy implements the network-wide reference name policy. Protocol,
// admission, alias traversal, rebinding, and DNS answer synthesis belong to the
// resolver pipeline, not this matcher.
package policy

type Kind string

const (
	Exact    Kind = "exact"
	Suffix   Kind = "suffix"
	Wildcard Kind = "wildcard"
	Glob     Kind = "glob"
	Regex    Kind = "regex"
)

type Class string

const (
	CustomAllow       Class = "custom-allow"
	CustomDeny        Class = "custom-deny"
	SubscriptionAllow Class = "subscription-allow"
	SubscriptionDeny  Class = "subscription-deny"
	SpecialDeny       Class = "special-deny"
)

// Rule is compiler input. ID is globally unique and stable; SourceID identifies
// provenance. SourceText retains the original diagnostic text. Empty Dialect
// means Go for Regex; other forms do not accept a dialect. Only enabled source
// rules (including explicitly enabled special rules) should be supplied.
type Rule struct {
	ID, SourceID, SourceText string
	Kind                     Kind
	Class                    Class
	Pattern                  string
	Dialect                  string
}

type Result string

const (
	Forward Result = "forward"
	Allow   Result = "allow"
	Block   Result = "block"
	Local   Result = "local"
	Paused  Result = "paused"
)

type Decision struct {
	Result     Result
	Generation uint64
	RuleID     string
	SourceIDs  []string
}

// Query evaluates Name, either the original question or a relevant alias.
// Original must be the same original question for the entire chain. Local is
// supplied by local-zone routing; Paused by the policy pause controller. Neither
// these flags nor Allow bypass ACL, DNS validation, or rebinding checks.
type Query struct {
	Original, Name         Name
	Local, Paused, Explain bool
}

// Limits bound configuration compilation, never elapsed request time. The total
// regex budget uses conservative accounting for expanded instructions, rune
// tables, expression storage and per-regexp overhead; it is not measured RSS.
// The benchmark task must calibrate the provisional program/memory defaults.
type Limits struct {
	MaxRegex, MaxExpressionBytes, MaxProgramInstructions int
	MaxTotalRegexBytes                                   int64
}

func DefaultLimits() Limits {
	return Limits{1024, 4096, 8192, 16 << 20}
}
