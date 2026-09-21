package policy

import (
	"fmt"
	"hash/maphash"
	"math"
	"slices"
	"strings"
	"unsafe"
)

// SnapshotOptions bounds retained accounting bytes, not process RSS or transient
// build allocations. The caller must admit only one build and budget live old
// generations plus input/parser storage. DisabledSources is read only during build.
type SnapshotOptions struct {
	MaxRules        int
	MaxBytes        uint64
	DisabledSources map[string]bool
}

func DefaultSnapshotOptions() SnapshotOptions {
	return SnapshotOptions{MaxRules: 2000000, MaxBytes: 512 << 20}
}

// SnapshotMemory counts backing capacities and fixed metadata, including all
// provenance. FallbackBytes includes conservative regex accounting (not exact
// regexp heap size); allocator rounding/runtime metadata are measured separately.
type SnapshotMemory struct {
	TotalBytes, ProvenanceBytes, ExactBytes, SuffixBytes, FallbackBytes uint64
	Rules                                                               int
}
type PolicySnapshot struct {
	generation  uint64
	rules       []ruleMeta
	provenance  string
	exact       exactIndex
	suffix      suffixIndex
	fallback    []compiledRule
	fallbackIDs []uint32
	memory      SnapshotMemory
}

func CompileSnapshot(g uint64, r []Rule, l Limits) (*PolicySnapshot, error) {
	return CompileSnapshotWithOptions(g, r, l, DefaultSnapshotOptions())
}
func CompileSnapshotWithOptions(g uint64, r []Rule, l Limits, o SnapshotOptions) (*PolicySnapshot, error) {
	return compileSnapshot(g, r, l, o, 70)
}

func compileSnapshot(g uint64, input []Rule, limits Limits, o SnapshotOptions, load int) (*PolicySnapshot, error) {
	if _, err := Compile(g, nil, limits); err != nil {
		return nil, err
	}
	if o.MaxRules <= 0 || o.MaxBytes == 0 || o.MaxBytes > math.MaxUint32 {
		return nil, fmt.Errorf("policy: invalid snapshot budget")
	}
	count, exactCount := 0, 0
	var textBytes uint64
	for _, r := range input {
		if !o.DisabledSources[r.SourceID] {
			count++
			if r.Kind == Exact {
				exactCount++
			}
			textBytes += uint64(len(r.ID)) + uint64(len(r.SourceID)) + uint64(len(r.SourceText)) + uint64(len(r.Pattern)) + uint64(len(r.Dialect))
		}
	}
	if count > o.MaxRules || uint64(count) > math.MaxUint32-1 || textBytes > o.MaxBytes {
		return nil, fmt.Errorf("policy: snapshot input budget exceeded")
	}
	s := &PolicySnapshot{generation: g, rules: make([]ruleMeta, 0, count)}
	s.exact.seed = maphash.MakeSeed()
	if exactCount > 0 {
		s.exact.slots = make([]exactSlot, (uint64(exactCount)*100+uint64(load)-1)/uint64(load))
	}
	var text strings.Builder
	seen := make(map[string]bool, count)
	sources := make(map[string]textRef)
	var suffixes []suffixBuild
	var suffixKeyBytes uint64
	var fallback []Rule
	for _, r := range input {
		if o.DisabledSources[r.SourceID] {
			continue
		}
		if r.ID == "" || seen[r.ID] {
			return nil, fmt.Errorf("policy: empty or duplicate rule ID %q", r.ID)
		}
		seen[r.ID] = true
		if rank(r.Class) < 0 {
			return nil, fmt.Errorf("policy: rule %q: unsupported class", r.ID)
		}
		kind := slices.Index(snapshotKinds[:], r.Kind)
		if kind < 0 {
			return nil, fmt.Errorf("policy: unsupported rule form %q", r.Kind)
		}
		m := ruleMeta{class: uint8(rank(r.Class)), kind: uint8(kind)}
		m.id = putText(&text, r.ID)
		m.pattern = putText(&text, r.Pattern)
		if r.SourceText == r.Pattern {
			m.text = m.pattern
		} else {
			m.text = putText(&text, r.SourceText)
		}
		m.dialect = putText(&text, r.Dialect)
		var ok bool
		m.source, ok = sources[r.SourceID]
		if !ok {
			m.source = putText(&text, r.SourceID)
			sources[r.SourceID] = m.source
		}
		head := uint32(len(s.rules) + 1)
		if r.Kind == Exact || r.Kind == Suffix {
			if r.Dialect != "" {
				return nil, fmt.Errorf("policy: dialect only applies to regex")
			}
			n, err := NormalizeName(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("policy: rule %q: %w", r.ID, err)
			}
			for i := 0; i < len(n.wire); i += 1 + int(n.wire[i]) {
				m.score += 2
			}
			if r.Kind == Exact {
				m.score++
				m.next = s.exact.insert(n.wire, maphash.String(s.exact.seed, n.wire), head)
			} else {
				var buf [255]byte
				suffixKeyBytes += uint64(len(n.wire) + 1)
				if suffixKeyBytes > o.MaxBytes {
					return nil, fmt.Errorf("policy: suffix key budget exceeded")
				}
				suffixes = append(suffixes, suffixBuild{string(reverseName(n, &buf)), head})
			}
		} else {
			// regexp and wildcard-bearing glob labels can retain their input
			// strings. Detach a short pattern from a possibly huge feed buffer.
			r.Pattern = strings.Clone(r.Pattern)
			fallback = append(fallback, r)
			s.fallbackIDs = append(s.fallbackIDs, head)
		}
		s.rules = append(s.rules, m)
		// Bound key/provenance offsets before another append. Input text preflight
		// above prevents uint32 conversion overflow in putText.
		if uint64(text.Cap())+uint64(cap(s.exact.keys))+uint64(len(s.exact.slots))*16+uint64(cap(s.rules))*uint64(unsafe.Sizeof(ruleMeta{})) > o.MaxBytes {
			return nil, fmt.Errorf("policy: snapshot byte budget exceeded")
		}
	}
	s.suffix.build(suffixes, s.rules)
	m, err := Compile(g, fallback, limits)
	if err != nil {
		return nil, err
	}
	s.fallback = m.rules
	fallbackCharge := uint64(m.regexBytes)
	for i := range s.fallback {
		r := &s.fallback[i]
		fallbackCharge += uint64(len(r.name.wire)) + uint64(cap(r.labels))*16
		for _, label := range r.labels {
			fallbackCharge += uint64(len(label))
		}
		// Retain no original Rule strings in fallback; diagnostics live in arena.
		r.rule = Rule{}
	}
	s.provenance = text.String()
	s.memory = SnapshotMemory{Rules: count, ProvenanceBytes: uint64(text.Cap()) + uint64(cap(s.rules))*uint64(unsafe.Sizeof(ruleMeta{})), ExactBytes: uint64(cap(s.exact.slots))*16 + uint64(cap(s.exact.keys)), SuffixBytes: uint64(cap(s.suffix.entries))*8 + uint64(cap(s.suffix.keys)), FallbackBytes: uint64(cap(s.fallback))*uint64(unsafe.Sizeof(compiledRule{})) + uint64(cap(s.fallbackIDs))*4 + fallbackCharge}
	s.memory.TotalBytes = s.memory.ProvenanceBytes + s.memory.ExactBytes + s.memory.SuffixBytes + s.memory.FallbackBytes + uint64(unsafe.Sizeof(*s))
	if s.memory.TotalBytes > o.MaxBytes {
		return nil, fmt.Errorf("policy: snapshot byte budget exceeded")
	}
	return s, nil
}

func (s *PolicySnapshot) Memory() SnapshotMemory { return s.memory }
func (s *PolicySnapshot) Match(n Name) Decision  { return s.match(n, false) }
func (s *PolicySnapshot) match(n Name, explain bool) Decision {
	d := Decision{Result: Forward, Generation: s.generation}
	var winner uint32
	visit := func(head uint32) {
		for head != 0 {
			r := s.rules[head-1]
			if explain && r.source.size > 0 {
				d.SourceIDs = append(d.SourceIDs, s.text(r.source))
			}
			better := winner == 0
			if !better {
				w := s.rules[winner-1]
				better = r.class < w.class || r.class == w.class && (r.score > w.score || r.score == w.score && s.text(r.id) < s.text(w.id))
			}
			if better {
				winner = head
			}
			head = r.next
		}
	}
	visit(s.exact.find(n.wire, maphash.String(s.exact.seed, n.wire)))
	if len(s.suffix.entries) > 0 {
		var buf [255]byte
		rev := reverseName(n, &buf)
		for end := 0; end < len(rev); {
			end += 1 + int(rev[end])
			visit(s.suffix.find(string(rev[:end])))
		}
	}
	if len(s.fallback) > 0 {
		labels, display := n.labels(), n.Display()
		for i := range s.fallback {
			if s.fallback[i].matches(n, labels, display) {
				visit(s.fallbackIDs[i])
			}
		}
	}
	if winner != 0 {
		r := s.rules[winner-1]
		d.RuleID = s.text(r.id)
		d.Result = Block
		if r.class == 0 || r.class == 2 {
			d.Result = Allow
		}
	}
	if explain {
		slices.Sort(d.SourceIDs)
		d.SourceIDs = slices.Compact(d.SourceIDs)
	}
	return d
}

func (s *PolicySnapshot) Evaluate(q Query) Decision {
	d := s.match(q.Name, q.Explain)
	if q.Local {
		d.Result = Local
		d.RuleID = ""
		return d
	}
	if q.Paused {
		d.Result = Paused
		d.RuleID = ""
		return d
	}
	if q.Name != q.Original {
		original := s.match(q.Original, q.Explain)
		if original.Result == Allow {
			if q.Explain {
				original.SourceIDs = append(original.SourceIDs, d.SourceIDs...)
				slices.Sort(original.SourceIDs)
				original.SourceIDs = slices.Compact(original.SourceIDs)
			}
			return original
		}
	}
	return d
}
