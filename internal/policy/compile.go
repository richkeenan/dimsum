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
	// Selection views retain constant-size fallback availability summaries.
	noFallback, noBaseFallback bool
	selection                  *Selection
	scopes                     map[string]Scope // sparse owner metadata; subscription entries stay 32 bytes
	generation                 uint64
	rules                      []ruleMeta
	provenance                 string
	sharedText                 []textRef
	exact                      exactIndex
	suffix                     suffixIndex
	fallback                   []compiledRule
	fallbackIDs                []uint32
	memory                     SnapshotMemory
	// A non-nil base marks a two-layer root; all indexes above belong to owners.
	base      *PolicySnapshot
	resources snapshotResources
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
	count, exactCount, suffixCount := 0, 0, 0
	var suffixReserve uint64
	var textBytes, storedTextBytes uint64
	shared := map[string]uint32{"": 0}
	for _, r := range input {
		if !o.DisabledSources[r.SourceID] {
			count++
			if r.Kind == Exact {
				exactCount++
			} else if r.Kind == Suffix {
				suffixCount++
				// ASCII names need the presentation bytes plus a label-length
				// byte and an arena-length byte. This is only a sizing hint:
				// IDNA can change lengths, so validated appends may grow it.
				suffixReserve += uint64(min(len(r.Pattern), 253) + 2)
			}
			textBytes += uint64(len(r.ID)) + uint64(len(r.SourceID)) + uint64(len(r.SourceText)) + uint64(len(r.Pattern)) + uint64(len(r.Dialect)) + uint64(len(r.Scope.ID))
			if count > o.MaxRules || uint64(count) > math.MaxUint32-1 || textBytes > o.MaxBytes {
				return nil, fmt.Errorf("policy: snapshot input budget exceeded")
			}
			storedTextBytes += uint64(len(r.ID)) + uint64(len(r.Pattern))
			if r.SourceText != r.Pattern {
				storedTextBytes += uint64(len(r.SourceText))
			}
			for _, value := range []string{r.SourceID, r.Dialect, r.Scope.ID} {
				if _, ok := shared[value]; !ok {
					storedTextBytes += uint64(len(value))
					shared[value] = 0
				}
			}
		}
	}
	if count > o.MaxRules || uint64(count) > math.MaxUint32-1 || textBytes > o.MaxBytes {
		return nil, fmt.Errorf("policy: snapshot input budget exceeded")
	}
	s := &PolicySnapshot{generation: g, rules: make([]ruleMeta, 0, count)}
	s.resources.inputBytes = textBytes
	s.sharedText = make([]textRef, 1, len(shared))
	s.exact.seed = maphash.MakeSeed()
	if exactCount > 0 {
		s.exact.slots = make([]exactSlot, (uint64(exactCount)*100+uint64(load)-1)/uint64(load))
	}
	var text strings.Builder
	// Reserve actual stored text, not the conservative admission estimate:
	// source IDs are interned and SourceText often shares its Pattern. Growing
	// this arena repeatedly otherwise leaves large copied buffers for GC.
	text.Grow(int(storedTextBytes))
	clear(shared)
	shared[""] = 0
	intern := func(value string) uint32 {
		if index, ok := shared[value]; ok {
			return index
		}
		index := uint32(len(s.sharedText))
		s.sharedText = append(s.sharedText, putText(&text, value))
		shared[value] = index
		return index
	}
	seen := make(map[string]bool, count)
	suffixes := suffixBuild{
		entries: make([]suffixEntry, 0, suffixCount),
		keys:    make([]byte, 0, min(suffixReserve, o.MaxBytes)),
	}
	var suffixKeyBytes uint64
	var exactKeys exactBuildArena
	var fallback []Rule
	for _, r := range input {
		if o.DisabledSources[r.SourceID] {
			continue
		}
		if err := validateRuleScope(r); err != nil {
			return nil, fmt.Errorf("policy: rule %q: %w", r.ID, err)
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
		s.resources.classes |= 1 << m.class
		s.resources.namespaces |= ruleNamespace(r.ID)
		m.off = uint32(text.Len())
		m.idSize = uint32(len(r.ID))
		m.patternSize = uint32(len(r.Pattern))
		text.WriteString(r.ID)
		text.WriteString(r.Pattern)
		if r.SourceText == r.Pattern {
			m.flags |= sourceTextIsPattern
		} else {
			m.textSize = uint32(len(r.SourceText))
			text.WriteString(r.SourceText)
		}
		m.source = intern(r.SourceID)
		m.dialect = intern(r.Dialect)
		if r.Scope.Kind != NetworkScope {
			if s.scopes == nil {
				s.scopes = make(map[string]Scope)
			}
			s.scopes[strings.Clone(r.ID)] = Scope{Kind: r.Scope.Kind, ID: strings.Clone(r.Scope.ID)}
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
				m.next, err = s.exact.insertStaged(n.wire, maphash.String(s.exact.seed, n.wire), head, &exactKeys, o.MaxBytes)
				if err != nil {
					return nil, err
				}
			} else {
				var buf [255]byte
				suffixKeyBytes += uint64(len(n.wire) + 1)
				if suffixKeyBytes > o.MaxBytes {
					return nil, fmt.Errorf("policy: suffix key budget exceeded")
				}
				suffixes.add(reverseName(n, &buf), head)
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
		if uint64(text.Cap())+uint64(cap(s.sharedText))*8+exactKeys.size()+uint64(len(s.exact.slots))*16+uint64(cap(s.rules))*uint64(unsafe.Sizeof(ruleMeta{})) > o.MaxBytes {
			return nil, fmt.Errorf("policy: snapshot byte budget exceeded")
		}
	}
	s.exact.keys = exactKeys.finish()
	exactKeys = exactBuildArena{}
	s.suffix.build(suffixes, s.rules)
	m, err := Compile(g, fallback, limits)
	if err != nil {
		return nil, err
	}
	s.fallback = m.rules
	s.resources.regexCount = m.regexCount
	s.resources.regexBytes = m.regexBytes
	s.resources.maxExpressionBytes = m.maxExpressionBytes
	s.resources.maxProgramInstructions = m.maxProgramInstructions
	fallbackCharge := uint64(m.regexBytes)
	for i := range s.fallback {
		r := &s.fallback[i]
		if r.kind == Glob {
			// normalizeGlob can leave wildcard labels in the original pattern
			// and literal labels in length-prefixed Name.wire storage. Detach
			// both: visible lengths below must cover all retained label bytes,
			// even when IDNA removes most of a long original literal label.
			for j, label := range r.labels {
				r.labels[j] = strings.Clone(label)
			}
		}
		fallbackCharge += uint64(len(r.name.wire)) + uint64(cap(r.labels))*16
		for _, label := range r.labels {
			fallbackCharge += uint64(len(label))
		}
		// Retain no original Rule strings in fallback; diagnostics live in arena.
		r.rule = Rule{}
	}
	s.provenance = text.String()
	s.memory = SnapshotMemory{Rules: count, ProvenanceBytes: uint64(text.Cap()) + uint64(cap(s.sharedText))*8 + uint64(cap(s.rules))*uint64(unsafe.Sizeof(ruleMeta{})), ExactBytes: uint64(cap(s.exact.slots))*16 + uint64(cap(s.exact.keys)), SuffixBytes: uint64(cap(s.suffix.entries))*8 + uint64(cap(s.suffix.keys)), FallbackBytes: uint64(cap(s.fallback))*uint64(unsafe.Sizeof(compiledRule{})) + uint64(cap(s.fallbackIDs))*4 + fallbackCharge}
	s.memory.TotalBytes = s.memory.ProvenanceBytes + s.memory.ExactBytes + s.memory.SuffixBytes + s.memory.FallbackBytes + uint64(unsafe.Sizeof(*s))
	for id, scope := range s.scopes {
		s.memory.ProvenanceBytes += uint64(128 + len(id) + len(scope.ID))
		s.memory.TotalBytes += uint64(128 + len(id) + len(scope.ID))
	}
	if s.memory.TotalBytes > o.MaxBytes {
		return nil, fmt.Errorf("policy: snapshot byte budget exceeded")
	}
	return s, nil
}

func (s *PolicySnapshot) Memory() SnapshotMemory { return s.memory }
func (s *PolicySnapshot) Match(n Name) Decision  { return s.match(n, false) }
func (s *PolicySnapshot) match(n Name, explain bool) Decision {
	d, _ := s.matchNumber(n, explain)
	return d
}

func (s *PolicySnapshot) matchNumber(n Name, explain bool) (Decision, uint32) {
	if s.base != nil {
		return s.matchOverlay(n, explain)
	}
	return s.matchOwnNumber(n, explain)
}

func (s *PolicySnapshot) matchOwnNumber(n Name, explain bool) (Decision, uint32) {
	return s.matchSelectedNumber(n, explain, s.selection, s.noFallback)
}

func (s *PolicySnapshot) matchSelectedNumber(n Name, explain bool, selection *Selection, noFallback bool) (Decision, uint32) {
	d := Decision{Result: Forward, Generation: s.generation}
	var winner uint32
	visit := func(head uint32) {
		for head != 0 {
			r := s.rules[head-1]
			if !selection.eligible(s.scope(r), snapshotClasses[r.class], s.text(s.sharedText[r.source])) {
				head = r.next
				continue
			}
			if explain && r.source != 0 {
				d.SourceIDs = append(d.SourceIDs, s.text(s.sharedText[r.source]))
			}
			better := winner == 0
			if !better {
				w := s.rules[winner-1]
				rs, ws := scopeRank(s.scope(r).Kind), scopeRank(s.scope(w).Kind)
				better = rs > ws || rs == ws && (r.class < w.class || r.class == w.class && (r.score > w.score || r.score == w.score && s.text(r.idRef()) < s.text(w.idRef())))
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
			visit(s.suffix.find(rev[:end]))
		}
	}
	if !noFallback && len(s.fallback) > 0 {
		var labels []string
		var display string
		for i := range s.fallback {
			r := s.rules[s.fallbackIDs[i]-1]
			if !selection.eligible(s.scope(r), snapshotClasses[r.class], s.text(s.sharedText[r.source])) {
				continue
			}
			if labels == nil {
				labels, display = n.labels(), n.Display()
			}
			if s.fallback[i].matches(n, labels, display) {
				visit(s.fallbackIDs[i])
			}
		}
	}
	if winner != 0 {
		r := s.rules[winner-1]
		d.RuleID = s.text(r.idRef())
		d.Scope = s.scope(r)
		d.Result = Block
		if r.class == 0 || r.class == 2 {
			d.Result = Allow
		}
	}
	if explain {
		slices.Sort(d.SourceIDs)
		d.SourceIDs = slices.Compact(d.SourceIDs)
	}
	return d, winner
}

func (s *PolicySnapshot) Evaluate(q Query) Decision {
	d, _ := s.EvaluateNumber(q)
	return d
}

// EvaluateNumber also returns the one-based immutable rule index. It is scoped
// to this generation and is suitable for fixed-size events, not as a public ID.
func (s *PolicySnapshot) EvaluateNumber(q Query) (Decision, uint32) {
	d, number := s.matchNumber(q.Name, q.Explain)
	if q.Local {
		d.Result = Local
		d.RuleID = ""
		d.Scope = Scope{}
		return d, 0
	}
	if q.Paused {
		d.Result = Paused
		d.RuleID = ""
		d.Scope = Scope{}
		return d, 0
	}
	if q.Name != q.Original {
		original, originalNumber := s.matchNumber(q.Original, q.Explain)
		if original.Result == Allow {
			if q.Explain {
				original.SourceIDs = append(original.SourceIDs, d.SourceIDs...)
				slices.Sort(original.SourceIDs)
				original.SourceIDs = slices.Compact(original.SourceIDs)
			}
			return original, originalNumber
		}
	}
	return d, number
}
