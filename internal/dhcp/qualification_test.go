package dhcp

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opt-in retained-state measurement. Keep the PRNG deterministic and state full:
// a decoder-only overload test cannot catch a retained identity index leak.
func TestQualificationStatePlateau(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_QUALIFY") != "1" {
		t.Skip("opt-in resource qualification")
	}
	t.Logf("toolchain=%s os=%s arch=%s CPUs=%d GOMAXPROCS=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	for _, capacity := range []int{1024, 4096} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			s := fixtureSettings()
			// RFC 2544's benchmarking block accommodates 4,096 synthetic
			// clients, unlike the three smaller RFC 5737 documentation blocks.
			s.ServerIP, s.Gateway, s.Subnet = "198.18.0.2", "198.18.0.1", "198.18.0.0/16"
			s.RangeStart, s.RangeEnd, s.MaxLeases = "198.18.1.0", "198.18.254.255", capacity
			e, now := engineFixture(t, s)
			for i := range capacity {
				r := Request{Type: Discover, MAC: [6]byte{2, 0, byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)}}
				o := offered(t, e, r)
				r.Type, r.RequestedIP, r.ServerID = RequestMessage, o.Address, netip.MustParseAddr(s.ServerIP)
				m := e.Handle(r).Mutation
				require.NotNil(t, m)
				require.NotNil(t, e.CompleteCommit(CommitResult{Token: m.Token}).Reply)
			}
			rng := rand.New(rand.NewPCG(8, 42))
			sample := func(n int) (runtime.MemStats, int, time.Duration) {
				start := time.Now()
				for range n {
					x := rng.Uint64()
					r := Request{Type: Discover, MAC: [6]byte{2, byte(x >> 32), byte(x >> 24), byte(x >> 16), byte(x >> 8), byte(x)}}
					e.Handle(r)
				}
				elapsed := time.Since(start)
				runtime.GC()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				return m, runtime.NumGoroutine(), elapsed
			}
			a, ga, da := sample(10000)
			b, gb, db := sample(100000)
			assert.Equal(t, capacity, len(e.Leases()))
			assert.LessOrEqual(t, gb, ga)
			assert.LessOrEqual(t, int64(b.HeapAlloc)-int64(a.HeapAlloc), int64(256<<10))
			t.Logf("capacity=%d randomized=10000+100000 heap=%d->%d goroutines=%d->%d elapsed=%s,%s allocated=%d gc=%d gc_cpu_fraction=%g clock=%s", capacity, a.HeapAlloc, b.HeapAlloc, ga, gb, da, db, b.TotalAlloc-a.TotalAlloc, b.NumGC-a.NumGC, b.GCCPUFraction, *now)
			runtime.KeepAlive(e)
		})
	}
}
