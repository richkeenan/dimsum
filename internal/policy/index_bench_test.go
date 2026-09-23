package policy

import (
	"fmt"
	"hash/maphash"
	"math/rand"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var benchmarkDecision Decision
var benchmarkHead uint32

func BenchmarkSnapshotGlobBuild(b *testing.B) {
	for _, tc := range []struct{ name, pattern string }{
		{"plain", "ads?.example.test"},
		{"idna-mapped-8MiB", "ads?.ex" + strings.Repeat("\u00ad", 4<<20) + "ample.test"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			rules := []Rule{{ID: "mapped", Kind: Glob, Class: SubscriptionDeny, Pattern: tc.pattern}}
			s, err := CompileSnapshot(1, rules, DefaultLimits())
			require.NoError(b, err)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s, err = CompileSnapshot(uint64(i+2), rules, DefaultLimits())
				if err != nil {
					break
				}
			}
			b.StopTimer()
			require.NoError(b, err)
			b.ReportMetric(float64(s.Memory().TotalBytes), "retained-B")
			b.ReportMetric(float64(s.Memory().FallbackBytes), "fallback-B")
		})
	}
}

// Deterministic synthetic feed: realistic stable membership IDs, verbatim text,
// eight source names and 23-byte canonical keys (root omitted). No network.
func benchmarkRules(n int, mixed bool) []Rule {
	rules := make([]Rule, n)
	for i := range rules {
		pattern := fmt.Sprintf("ad%07d.zone%03d.test", i, i%100)
		kind := Exact
		if mixed && i%2 == 0 {
			kind = Suffix
		}
		rules[i] = Rule{ID: fmt.Sprintf("feed%d:%d:0", i%8, i), SourceID: fmt.Sprintf("feed%d", i%8), SourceText: pattern, Pattern: pattern, Kind: kind, Class: SubscriptionDeny}
	}
	return rules
}

func BenchmarkSnapshot(b *testing.B) {
	for _, n := range []int{80000, 250000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rules := benchmarkRules(n, true)
			s, err := CompileSnapshot(1, rules, DefaultLimits())
			require.NoError(b, err)
			for _, text := range []string{rules[n/2+1].Pattern, "child." + rules[n/2].Pattern, "absent.zone999.test"} {
				name, err := NormalizeName(text)
				require.NoError(b, err)
				b.Run(text, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						benchmarkDecision = s.Match(name)
					}
					b.ReportMetric(float64(s.Memory().TotalBytes)/float64(n), "retained-B/rule")
				})
			}
		})
	}
}

func BenchmarkIndexCandidates(b *testing.B) {
	for _, n := range []int{80000, 250000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rules := benchmarkRules(n, false)
			names := make([]Name, n)
			for i, r := range rules {
				var err error
				names[i], err = NormalizeName(r.Pattern)
				require.NoError(b, err)
			}
			for _, load := range []int{70, 80} {
				b.Run(fmt.Sprintf("hash%d", load), func(b *testing.B) {
					x := exactIndex{seed: maphash.MakeSeed(), slots: make([]exactSlot, (n*100+load-1)/load)}
					for i, name := range names {
						x.insert(name.wire, maphash.String(x.seed, name.wire), uint32(i+1))
					}
					for _, hit := range []bool{true, false} {
						b.Run(fmt.Sprint(hit), func(b *testing.B) {
							key := names[n/2].wire
							if !hit {
								key = "\x06absent\x04test"
							}
							b.ReportAllocs()
							b.ResetTimer()
							for i := 0; i < b.N; i++ {
								benchmarkHead = x.find(key, maphash.String(x.seed, key))
							}
							b.ReportMetric(float64(cap(x.slots)*16+cap(x.keys))/float64(n), "index-B/rule")
						})
					}
				})
			}
			b.Run("reversed-sorted", func(b *testing.B) {
				var x suffixIndex
				in := suffixBuild{entries: make([]suffixEntry, 0, n)}
				meta := make([]ruleMeta, n)
				for i, name := range names {
					var buf [255]byte
					in.add(reverseName(name, &buf), uint32(i+1))
				}
				x.build(in, meta)
				for _, hit := range []bool{true, false} {
					b.Run(fmt.Sprint(hit), func(b *testing.B) {
						name := names[n/2]
						if !hit {
							name = Name{wire: "\x06absent\x04test"}
						}
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							var buf [255]byte
							benchmarkHead = x.find(reverseName(name, &buf))
						}
						b.ReportMetric(float64(cap(x.entries)*8+cap(x.keys))/float64(n), "index-B/rule")
					})
				}
			})
		})
	}
}

// Build includes one active generation and caller-owned input. Peak heap is
// sampled every millisecond: an observed lower bound, not an allocation proof.
// TotalAlloc/op is also reported by Go; peak includes unreachable garbage until
// GC. Sampler overhead is included in build ns/op, never lookup measurements.
func BenchmarkSnapshotBuild(b *testing.B) {
	for _, n := range []int{80000, 250000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rules := benchmarkRules(n, true)
			active, err := CompileSnapshot(1, rules, DefaultLimits())
			require.NoError(b, err)
			runtime.GC()
			var base runtime.MemStats
			runtime.ReadMemStats(&base)
			var peak atomic.Uint64
			peak.Store(base.HeapAlloc)
			done, stopped := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(stopped)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						var m runtime.MemStats
						runtime.ReadMemStats(&m)
						for p := peak.Load(); m.HeapAlloc > p && !peak.CompareAndSwap(p, m.HeapAlloc); p = peak.Load() {
						}
					}
				}
			}()
			b.ReportAllocs()
			b.ResetTimer()
			var built *PolicySnapshot
			for i := 0; i < b.N; i++ {
				built, err = CompileSnapshot(uint64(i+2), rules, DefaultLimits())
				if err != nil {
					break
				}
			}
			b.StopTimer()
			close(done)
			<-stopped
			require.NoError(b, err)
			var end runtime.MemStats
			runtime.ReadMemStats(&end)
			p := max(peak.Load(), end.HeapAlloc)
			b.ReportMetric(float64(base.HeapAlloc), "active-input-heap-B")
			b.ReportMetric(float64(p), "build-peak-heap-B")
			b.ReportMetric(float64(built.Memory().TotalBytes), "snapshot-B")
			b.ReportMetric(float64(built.Memory().ProvenanceBytes), "provenance-B")
			runtime.GC()
			runtime.ReadMemStats(&end)
			b.ReportMetric(float64(end.HeapAlloc), "active-new-input-heap-B")
			samples := []metrics.Sample{{Name: "/gc/scan/heap:bytes"}}
			metrics.Read(samples)
			b.ReportMetric(float64(samples[0].Value.Uint64()), "active-new-scan-B")
			runtime.KeepAlive(active)
			runtime.KeepAlive(built)
			runtime.KeepAlive(rules)
		})
	}
}

func BenchmarkReference(b *testing.B) {
	for _, n := range []int{80000, 250000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rules := benchmarkRules(n, true)
			m, err := Compile(1, rules, DefaultLimits())
			require.NoError(b, err)
			name, err := NormalizeName("absent.zone999.test")
			require.NoError(b, err)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkDecision = m.Match(name)
			}
		})
	}
}

func BenchmarkSuffixCandidates(b *testing.B) {
	for _, n := range []int{80000, 250000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rules := benchmarkRules(n, false)
			names := make([]Name, n)
			for i, r := range rules {
				var err error
				names[i], err = NormalizeName(r.Pattern)
				require.NoError(b, err)
			}
			queries := make([]Name, 1024)
			rng := rand.New(rand.NewSource(17))
			for i := range queries {
				text := "child." + rules[rng.Intn(n)].Pattern
				if i%2 == 0 {
					text = fmt.Sprintf("absent%d.zone%03d.test", i, i%100)
				}
				var err error
				queries[i], err = NormalizeName(text)
				require.NoError(b, err)
			}
			for _, load := range []int{70, 80} {
				b.Run(fmt.Sprintf("hash-walk%d", load), func(b *testing.B) {
					x := exactIndex{seed: maphash.MakeSeed(), slots: make([]exactSlot, (n*100+load-1)/load)}
					for i, name := range names {
						x.insert(name.wire, maphash.String(x.seed, name.wire), uint32(i+1))
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						name := queries[i%len(queries)]
						var head uint32
						for start := 0; start < len(name.wire); start += 1 + int(name.wire[start]) {
							head += x.find(name.wire[start:], maphash.String(x.seed, name.wire[start:]))
						}
						benchmarkHead = head
					}
					b.ReportMetric(float64(cap(x.slots)*16+cap(x.keys))/float64(n), "index-B/rule")
				})
			}
			b.Run("reversed-sorted", func(b *testing.B) {
				var x suffixIndex
				in := suffixBuild{entries: make([]suffixEntry, 0, n)}
				meta := make([]ruleMeta, n)
				for i, name := range names {
					var buf [255]byte
					in.add(reverseName(name, &buf), uint32(i+1))
				}
				x.build(in, meta)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var buf [255]byte
					rev := reverseName(queries[i%len(queries)], &buf)
					var head uint32
					for end := 0; end < len(rev); {
						end += 1 + int(rev[end])
						head += x.find(rev[:end])
					}
					benchmarkHead = head
				}
				b.ReportMetric(float64(cap(x.entries)*8+cap(x.keys))/float64(n), "index-B/rule")
			})
		})
	}
}

func BenchmarkSnapshotWorkingSet(b *testing.B) {
	for _, n := range []int{80000, 250000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rules := benchmarkRules(n, true)
			s, err := CompileSnapshot(1, rules, DefaultLimits())
			require.NoError(b, err)
			queries := make([]Name, 2048)
			rng := rand.New(rand.NewSource(23))
			for i := range queries {
				id := rng.Intn(n)
				text := rules[id].Pattern
				switch i % 3 {
				case 0:
					text = fmt.Sprintf("absent%d.zone%03d.test", i, i%100)
				case 1:
					id &^= 1
					text = "child." + rules[id].Pattern
				}
				queries[i], err = NormalizeName(text)
				require.NoError(b, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkDecision = s.Match(queries[i%len(queries)])
			}
			b.ReportMetric(float64(s.Memory().TotalBytes)/float64(n), "retained-B/rule")
		})
	}
}
