package policy

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var rules []Rule
	var names []Name
	classes := []Class{CustomAllow, CustomDeny, SubscriptionAllow, SubscriptionDeny, SpecialDeny}
	for i := range 350 {
		pattern := fmt.Sprintf("n%d.zone%d.example", rng.Intn(80), rng.Intn(5))
		kind := []Kind{Exact, Suffix, Wildcard, Glob, Regex}[i%5]
		switch kind {
		case Wildcard:
			pattern = "*." + pattern
		case Glob:
			pattern = "n?." + pattern
		case Regex:
			pattern = `(^|\.)n[12]\.zone[01]\.example$`
		}
		rules = append(rules, Rule{ID: fmt.Sprintf("rule%04d", i), SourceID: fmt.Sprintf("source%d", i%7), SourceText: pattern, Kind: kind, Class: classes[rng.Intn(len(classes))], Pattern: pattern})
	}
	rules = append(rules, Rule{ID: "idna", SourceID: "unicode", Kind: Suffix, Class: SubscriptionDeny, Pattern: "BÜCHER。Example。"})
	for i := range 400 {
		n, err := NormalizeName(fmt.Sprintf("n%d.zone%d.example", rng.Intn(80), rng.Intn(5)))
		require.NoError(t, err)
		names = append(names, n)
		n, err = NameFromWire(append(append([]byte{3, byte(i), '.', 0}, []byte(n.wire)...), 0))
		require.NoError(t, err)
		names = append(names, n)
	}
	for _, text := range []string{"bücher.example", "x.xn--bcher-kva.example", "absent.test"} {
		n, err := NormalizeName(text)
		require.NoError(t, err)
		names = append(names, n)
	}
	names = append(names, Name{})
	for _, disabled := range []map[string]bool{nil, {"source1": true, "source3": true}, {"unicode": true}} {
		opts := DefaultSnapshotOptions()
		opts.DisabledSources = disabled
		got, err := CompileSnapshotWithOptions(42, rules, DefaultLimits(), opts)
		require.NoError(t, err)
		var enabled []Rule
		for _, r := range rules {
			if !disabled[r.SourceID] {
				enabled = append(enabled, r)
			}
		}
		ref, err := Compile(42, enabled, DefaultLimits())
		require.NoError(t, err)
		for i, n := range names {
			assert.Equal(t, ref.Match(n), got.Match(n))
			q := Query{Original: names[(i+3)%len(names)], Name: n, Explain: true, Local: i%19 == 0, Paused: i%23 == 0}
			assert.Equal(t, ref.Evaluate(q), got.Evaluate(q))
		}
		for _, r := range enabled {
			actual, ok := got.Rule(r.ID)
			assert.True(t, ok)
			assert.Equal(t, r, actual)
		}
	}
}

func TestCompileSnapshotIsolationAndBudgets(t *testing.T) {
	rules := []Rule{{ID: "a", SourceID: "one", SourceText: "original", Kind: Exact, Class: SubscriptionDeny, Pattern: "example.com"}, {ID: "b", SourceID: "two", Kind: Exact, Class: SubscriptionDeny, Pattern: "example.com"}}
	s, err := CompileSnapshot(1, rules, DefaultLimits())
	require.NoError(t, err)
	n, err := NormalizeName("example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, s.Evaluate(Query{Original: n, Name: n, Explain: true}).SourceIDs)
	opts := DefaultSnapshotOptions()
	opts.DisabledSources = map[string]bool{"one": true}
	s2, err := CompileSnapshotWithOptions(2, rules, DefaultLimits(), opts)
	require.NoError(t, err)
	assert.Equal(t, "b", s2.Match(n).RuleID)
	opts.DisabledSources["two"] = true
	s3, err := CompileSnapshotWithOptions(3, rules, DefaultLimits(), opts)
	require.NoError(t, err)
	assert.Equal(t, Forward, s3.Match(n).Result)
	rules[0].Pattern = "changed.test"
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				if s.Match(n).RuleID != "a" || s2.Match(n).RuleID != "b" {
					errs <- "changed snapshot"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		assert.Fail(t, e)
	}
	opts = DefaultSnapshotOptions()
	opts.MaxRules = 1
	bad, err := CompileSnapshotWithOptions(3, rules, DefaultLimits(), opts)
	require.Error(t, err)
	assert.Nil(t, bad)
	opts = DefaultSnapshotOptions()
	opts.MaxBytes = 1
	bad, err = CompileSnapshotWithOptions(3, rules, DefaultLimits(), opts)
	require.Error(t, err)
	assert.Nil(t, bad)
	assert.Greater(t, s.Memory().ProvenanceBytes, uint64(0))
	assert.Greater(t, s.Memory().TotalBytes, s.Memory().ProvenanceBytes)
	for _, invalid := range []Rule{{ID: "bad", Kind: Exact, Class: SubscriptionDeny, Pattern: "bad..test"}, {ID: "bad", Kind: Exact, Class: SubscriptionDeny, Pattern: "good.test", Dialect: "go"}, {ID: "bad", Kind: Suffix, Class: "unknown", Pattern: "good.test"}} {
		bad, err := CompileSnapshot(3, []Rule{invalid}, DefaultLimits())
		require.Error(t, err)
		assert.Nil(t, bad)
	}
	_, err = CompileSnapshot(3, []Rule{rules[0], rules[0]}, DefaultLimits())
	require.Error(t, err)
}

func TestIndexFullHashCollision(t *testing.T) {
	x := exactIndex{slots: make([]exactSlot, 5)}
	// Deliberately identical 64-bit hashes, not merely identical buckets.
	assert.Zero(t, x.insert("\x03one\x04test", 77, 1))
	assert.Zero(t, x.insert("\x03two\x04test", 77, 2))
	assert.Equal(t, uint32(1), x.find("\x03one\x04test", 77))
	assert.Equal(t, uint32(2), x.find("\x03two\x04test", 77))
	assert.Zero(t, x.find("\x03bad\x04test", 77), "hash equality must never block an absent name")
	assert.Zero(t, x.find("\x08one.test", 77), "literal dot is not a label boundary")
	assert.Equal(t, uint32(1), x.insert("\x03one\x04test", 77, 3))
	assert.Equal(t, uint32(3), x.find("\x03one\x04test", 77))
}

func TestIndexLookupAllocation(t *testing.T) {
	s, err := CompileSnapshot(1, []Rule{{ID: "exact", Kind: Exact, Class: SubscriptionDeny, Pattern: "exact.test"}, {ID: "suffix", Kind: Suffix, Class: SubscriptionDeny, Pattern: "suffix.test"}}, DefaultLimits())
	require.NoError(t, err)
	for _, text := range []string{"exact.test", "child.suffix.test", "absent.test"} {
		n, err := NormalizeName(text)
		require.NoError(t, err)
		var decision Decision
		allocs := testing.AllocsPerRun(100, func() { decision = s.Match(n) })
		assert.Zero(t, allocs, text)
		assert.Equal(t, uint64(1), decision.Generation)
	}
}

func TestCompileWideProvenanceAndDNSBounds(t *testing.T) {
	assert.Equal(t, uintptr(16), unsafe.Sizeof(exactSlot{}))
	assert.Equal(t, uintptr(8), unsafe.Sizeof(suffixEntry{}))
	assert.Equal(t, uintptr(48), unsafe.Sizeof(ruleMeta{}))
	rules := make([]Rule, 65537)
	for i := range rules {
		rules[i] = Rule{ID: fmt.Sprintf("r%06d", i), SourceID: fmt.Sprintf("s%06d", i), Kind: Exact, Class: SubscriptionDeny, Pattern: "duplicate.test"}
	}
	rules[len(rules)-1].Class = CustomAllow
	s, err := CompileSnapshot(7, rules, DefaultLimits())
	require.NoError(t, err)
	n, err := NormalizeName("duplicate.test")
	require.NoError(t, err)
	d := s.Evaluate(Query{Original: n, Name: n, Explain: true})
	assert.Equal(t, Allow, d.Result)
	assert.Equal(t, "r065536", d.RuleID)
	assert.Len(t, d.SourceIDs, len(rules))
	assert.Contains(t, d.SourceIDs, "s065536")
	for _, pattern := range []string{strings.Repeat("a.", 126) + "a", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)} {
		rules := []Rule{{ID: "deep-suffix", Kind: Suffix, Class: SubscriptionDeny, Pattern: pattern}, {ID: "deep-exact", Kind: Exact, Class: SubscriptionDeny, Pattern: pattern}}
		ref, err := Compile(1, rules, DefaultLimits())
		require.NoError(t, err)
		got, err := CompileSnapshot(1, rules, DefaultLimits())
		require.NoError(t, err)
		n, err := NormalizeName(pattern)
		require.NoError(t, err)
		assert.Equal(t, ref.Match(n), got.Match(n))
	}
}
