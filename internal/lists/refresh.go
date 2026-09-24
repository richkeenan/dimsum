package lists

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/richkeenan/dimsum/internal/policy"
)

type Subscription struct {
	ID         string      `yaml:"id"`
	URL        string      `yaml:"url"`
	Dialect    Dialect     `yaml:"dialect"`
	DomainKind policy.Kind `yaml:"domain_kind"`
	Enabled    bool        `yaml:"enabled"`
	// DefaultApply controls network application, independently of download availability.
	// Omission preserves the legacy enabled-source behavior.
	DefaultApply *bool `yaml:"default_apply,omitempty" json:"DefaultApply,omitempty"`
	// A deletion of >= 50% is quarantined until explicitly approved in text.
	AllowLargeDeletion bool `yaml:"allow_large_deletion,omitempty"`
}
type Version struct {
	Rules           []policy.Rule
	SHA256, Warning string
}
type sourceArtifact struct {
	Source              Source
	URL, ETag, Modified string
	Text                []byte
}

func parseWarning(r Result) string {
	if r.Rejected == 0 {
		return ""
	}
	first := r.Diagnostics[0]
	return fmt.Sprintf("skipped %d invalid domain line(s); line %d: %s (%s)", r.Rejected, first.Line, first.Text, first.Code)
}

// Refresh retains source-level LKG on every failed candidate. The coordinator
// calls sources serially: one download/parser/build at a time, no unbounded jobs.
func (f *Fetcher) Refresh(ctx context.Context, dir string, s Subscription, offline bool) (Version, error) {
	source := Source{ID: s.ID, Dialect: s.Dialect, DomainKind: s.DomainKind}
	if s.URL == WorkCompatibilityURL {
		if s.Dialect != Adblock || s.DomainKind != policy.Suffix {
			return Version{}, fmt.Errorf("source %s: built-in work list requires dns-adblock and suffix", s.ID)
		}
		parsed, err := Parse(strings.NewReader(workCompatibility), source, DefaultLimits())
		if err != nil {
			return Version{}, fmt.Errorf("source %s: %w", s.ID, err)
		}
		return Version{Rules: parsed.Rules, SHA256: parsed.SHA256, Warning: parseWarning(parsed)}, nil
	}
	identity, _ := json.Marshal(struct {
		Source Source
		URL    string
	}{source, s.URL})
	key := fmt.Sprintf("%x", sha256.Sum256(identity))
	path := filepath.Join(dir, key+".source")
	var old *sourceArtifact
	var previous Result
	if b, e := ReadArtifact(path, 180<<20); e == nil {
		var a sourceArtifact
		if json.Unmarshal(b, &a) == nil && a.Source == source && a.URL == s.URL {
			if parsed, e := Parse(bytes.NewReader(a.Text), source, DefaultLimits()); e == nil {
				old = &a
				previous = parsed
			}
		}
	}
	fallback := func(err error) (Version, error) {
		if old != nil {
			return Version{Rules: previous.Rules, SHA256: previous.SHA256, Warning: strings.TrimSuffix(err.Error()+"; "+parseWarning(previous), "; ")}, nil
		}
		return Version{}, fmt.Errorf("source %s: %w", s.ID, err)
	}
	if offline {
		return fallback(fmt.Errorf("offline: retained source"))
	}
	body, etag, modified, unchanged, err := f.fetch(ctx, s, old)
	if err != nil {
		return fallback(err)
	}
	if unchanged {
		return Version{Rules: previous.Rules, SHA256: previous.SHA256, Warning: parseWarning(previous)}, nil
	}
	parsed, err := Parse(bytes.NewReader(body), source, DefaultLimits())
	if err != nil {
		if len(parsed.Diagnostics) > 0 {
			first := parsed.Diagnostics[0]
			err = fmt.Errorf("%w; line %d: %s (%s)", err, first.Line, first.Text, first.Code)
		}
		return fallback(err)
	}
	if old != nil && parsed.Accepted <= previous.Accepted/2 && !s.AllowLargeDeletion {
		return fallback(fmt.Errorf("source deletion requires review: %d -> %d rules", previous.Accepted, parsed.Accepted))
	}
	a := sourceArtifact{Source: source, URL: s.URL, ETag: etag, Modified: modified, Text: body}
	b, err := json.Marshal(a)
	if err != nil {
		return fallback(err)
	}
	if err = WriteArtifact(path, b); err != nil {
		return fallback(err)
	}
	return Version{Rules: parsed.Rules, SHA256: parsed.SHA256, Warning: parseWarning(parsed)}, nil
}
