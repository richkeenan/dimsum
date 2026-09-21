// Package lists parses bounded subscription candidates without publishing them.
package lists

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/richkeenan/dimsum/internal/policy"
)

type Dialect string

const (
	Hosts   Dialect = "hosts"
	Domains Dialect = "domains"
	Adblock Dialect = "dns-adblock"
)

type Source struct {
	ID         string
	Dialect    Dialect
	DomainKind policy.Kind // Required Exact/Suffix for plain entries; hosts are always exact.
}

// Limits bound expanded input and retained output. The caller bounds compressed
// transfer, sets reader deadlines, and owns cancellation and publication.
type Limits struct {
	MaxBytes                               int64
	MaxLineBytes, MaxRules, MaxDiagnostics int
}

func DefaultLimits() Limits { return Limits{128 << 20, 64 << 10, 2000000, 100} }

type Diagnostic struct {
	Line       int
	Text, Code string
}
type Result struct {
	Rules                              []policy.Rule
	Diagnostics                        []Diagnostic
	Lines, Accepted, Rejected, Skipped int
	SHA256                             string
}

// Parse returns no Rules on any error, including unsupported exceptions. Counts
// and diagnostics describe the quarantined candidate, never an active source.
// SHA256 covers original bytes and is set only after a complete bounded read.
// SourceText excludes CR/LF terminators but preserves other original line bytes.
// IDs encode source ID, physical line, and alias index; stable for identical
// source bytes, not across line insertion. Duplicate memberships are retained.
func Parse(r io.Reader, s Source, l Limits) (out Result, err error) {
	defer func() {
		if err != nil {
			out.Rules = nil
		}
	}()
	if r == nil || s.ID == "" || l.MaxBytes <= 0 || l.MaxBytes == math.MaxInt64 || l.MaxLineBytes <= 0 || l.MaxLineBytes > math.MaxInt-2 || l.MaxRules <= 0 || l.MaxDiagnostics <= 0 {
		return out, fmt.Errorf("lists: invalid source or limits")
	}
	if s.Dialect != Hosts && s.Dialect != Domains && s.Dialect != Adblock {
		return out, fmt.Errorf("lists: unknown dialect %q", s.Dialect)
	}
	if s.Dialect != Hosts && s.DomainKind != policy.Exact && s.DomainKind != policy.Suffix {
		return out, fmt.Errorf("lists: explicit domain interpretation required")
	}
	h := sha256.New()
	bounded := &io.LimitedReader{R: r, N: l.MaxBytes + 1}
	scanner := bufio.NewScanner(io.TeeReader(bounded, h))
	scanner.Buffer(make([]byte, min(4096, l.MaxLineBytes+2)), l.MaxLineBytes+2)
	for scanner.Scan() {
		out.Lines++
		raw := scanner.Text()
		if len(raw) > l.MaxLineBytes {
			return out, fmt.Errorf("lists: line %d exceeds limit", out.Lines)
		}
		text := raw
		if out.Lines == 1 {
			text = strings.TrimPrefix(text, "\ufeff")
		}
		text = strings.TrimSpace(text)
		var rules []policy.Rule
		var code string
		switch s.Dialect {
		case Hosts:
			rules, code = parseHosts(text)
		case Domains:
			rules, code = parseDomains(text, s.DomainKind)
		case Adblock:
			rules, code = parseAdblock(text, s.DomainKind)
		}
		if code != "" {
			out.Rejected++
			if len(out.Diagnostics) < l.MaxDiagnostics {
				out.Diagnostics = append(out.Diagnostics, Diagnostic{out.Lines, raw, code})
			}
			continue
		}
		if len(rules) == 0 {
			out.Skipped++
			continue
		}
		if len(rules) > l.MaxRules-out.Accepted {
			return out, fmt.Errorf("lists: rule limit exceeded at line %d", out.Lines)
		}
		for i := range rules {
			rules[i].SourceID = s.ID
			rules[i].SourceText = raw
			rules[i].ID = fmt.Sprintf("%d:%s:%d:%d", len(s.ID), s.ID, out.Lines, i)
		}
		out.Accepted += len(rules)
		out.Rules = append(out.Rules, rules...)
	}
	if e := scanner.Err(); e != nil {
		return out, fmt.Errorf("lists: read at line %d: %w", out.Lines+1, e)
	}
	if bounded.N == 0 {
		return out, fmt.Errorf("lists: byte limit exceeded")
	}
	out.SHA256 = hex.EncodeToString(h.Sum(nil))
	if out.Rejected > 0 {
		return out, fmt.Errorf("lists: %d unsupported or invalid lines", out.Rejected)
	}
	if out.Accepted == 0 {
		return out, fmt.Errorf("lists: empty source")
	}
	return out, nil
}
