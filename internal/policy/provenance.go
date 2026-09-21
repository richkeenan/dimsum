package policy

import "strings"

// No pointers in per-rule metadata. Membership is one entry per stable rule ID;
// duplicate owners share an index key, never lose their independent sources.
type textRef struct{ off, size uint32 }
type ruleMeta struct {
	id, source, text, pattern, dialect textRef
	next                               uint32 // index+1, zero terminates a duplicate-key membership chain
	class, kind, score                 uint8
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
		if s.text(r.id) == id {
			return Rule{ID: id, SourceID: s.text(r.source), SourceText: s.text(r.text), Pattern: s.text(r.pattern), Dialect: s.text(r.dialect), Class: snapshotClasses[r.class], Kind: snapshotKinds[r.kind]}, true
		}
	}
	return Rule{}, false
}
