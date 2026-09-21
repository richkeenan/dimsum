package policy

import "strings"

// No pointers in per-rule metadata. Membership is one entry per stable rule ID;
// duplicate owners share an index key, never lose their independent sources.
type textRef struct{ off, size uint32 }
type ruleMeta struct {
	// ID, pattern, and optional source text occupy one consecutive arena span.
	// Full 32-bit lengths preserve long IDs and original diagnostic text.
	off, idSize, patternSize, textSize uint32
	source, dialect                    uint32 // indexes into shared text references
	next                               uint32 // index+1, zero terminates a duplicate-key membership chain
	class, kind, score, flags          uint8
}

const sourceTextIsPattern uint8 = 1

func (r ruleMeta) idRef() textRef      { return textRef{r.off, r.idSize} }
func (r ruleMeta) patternRef() textRef { return textRef{r.off + r.idSize, r.patternSize} }
func (r ruleMeta) textRef() textRef {
	if r.flags&sourceTextIsPattern != 0 {
		return r.patternRef()
	}
	return textRef{r.off + r.idSize + r.patternSize, r.textSize}
}

func (s *PolicySnapshot) rule(r ruleMeta) Rule {
	return Rule{ID: s.text(r.idRef()), SourceID: s.text(s.sharedText[r.source]), SourceText: s.text(r.textRef()), Pattern: s.text(r.patternRef()), Dialect: s.text(s.sharedText[r.dialect]), Class: snapshotClasses[r.class], Kind: snapshotKinds[r.kind]}
}

var snapshotClasses = [...]Class{CustomAllow, CustomDeny, SubscriptionAllow, SubscriptionDeny, SpecialDeny}
var snapshotKinds = [...]Kind{Exact, Suffix, Wildcard, Glob, Regex}

func putText(b *strings.Builder, s string) textRef {
	r := textRef{uint32(b.Len()), uint32(len(s))}
	b.WriteString(s)
	return r
}
func (s *PolicySnapshot) text(r textRef) string {
	return s.provenance[r.off : uint64(r.off)+uint64(r.size)]
}

// Rule returns a value copy for diagnostics. This cold path intentionally scans
// compact IDs rather than retaining another million-entry diagnostic hash map.
func (s *PolicySnapshot) Rule(id string) (Rule, bool) {
	for _, r := range s.rules {
		if s.text(r.idRef()) == id {
			return s.rule(r), true
		}
	}
	return Rule{}, false
}

// RuleAt is a constant-time generation-scoped history lookup. Returned strings
// belong to the immutable snapshot; consumers retaining a subset should copy.
func (s *PolicySnapshot) RuleAt(number uint32) (Rule, bool) {
	if number == 0 || uint64(number) > uint64(len(s.rules)) {
		return Rule{}, false
	}
	r := s.rules[number-1]
	return s.rule(r), true
}
