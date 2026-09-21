package dnscache

import (
	"bytes"
	"fmt"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func fixture(t testing.TB, name string) (dnswire.Message, []byte) {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion(name, dns.TypeA)
	q.Id = 123
	w, err := q.Pack()
	require.NoError(t, err)
	var request dnswire.Message
	require.NoError(t, dnswire.ParseRequest(w, &request))
	r := new(dns.Msg)
	r.SetReply(q)
	r.Compress, r.AuthenticatedData = true, true
	a, err := dns.NewRR(name + " 120 IN A 192.0.2.1")
	require.NoError(t, err)
	r.Answer = []dns.RR{a, &dns.RFC3597{Hdr: dns.RR_Header{Name: name, Rrtype: 65400, Class: 1, Ttl: 60}, Rdata: "c001ff00"}}
	w, err = r.Pack()
	require.NoError(t, err)
	return request, w
}

func TestCacheReconstructionOwnershipExpiry(t *testing.T) {
	c, err := New(Config{Bytes: 1 << 20, Shards: 4})
	require.NoError(t, err)
	q, wire := fixture(t, "Example.org.")
	k, ok := NewKey(&q, 1, 2, 3)
	require.True(t, ok)
	now := time.Now()
	require.True(t, c.Put(k, wire, now))
	clear(wire)
	q.Question.Header.ID = 456
	q.Question.Name.Wire[1] = 'e'
	out := make([]byte, 512)
	r := c.Get(k, &q, out, now.Add(10*time.Second))
	require.True(t, r.Hit)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:r.Length]))
	assert.EqualValues(t, 456, got.Id)
	assert.Equal(t, "example.org.", got.Question[0].Name)
	assert.False(t, got.AuthenticatedData)
	assert.EqualValues(t, 110, got.Answer[0].Header().Ttl)
	assert.EqualValues(t, 50, got.Answer[1].Header().Ttl)
	assert.Equal(t, "c001ff00", got.Answer[1].(*dns.RFC3597).Rdata)
	clear(out)
	runtime.GC()
	assert.True(t, c.Get(k, &q, out, now).Hit)
	assert.False(t, c.Get(k, &q, out, now.Add(60*time.Second)).Hit)
	assert.LessOrEqual(t, c.Stats().RetainedBytes, 1<<20)
}

func TestCacheIsolationAndCollision(t *testing.T) {
	c, err := New(Config{Bytes: 1 << 20, Shards: 1})
	require.NoError(t, err)
	q, wire := fixture(t, "example.org.")
	k, ok := NewKey(&q, 1, 2, 3)
	require.True(t, ok)
	now := time.Now()
	require.True(t, c.put(k, wire, now, 7))
	other, otherWire := fixture(t, "different.org.")
	k2, _ := NewKey(&other, 1, 2, 3)
	out := bytes.Repeat([]byte{0xaa}, 512)
	assert.False(t, c.get(k2, &other, out, now, 7).Hit)
	assert.True(t, c.get(k, &q, out, now, 7).Hit)
	require.True(t, c.put(k2, otherWire, now, 7))
	assert.True(t, c.get(k, &q, out, now, 7).Hit)
	assert.True(t, c.get(k2, &other, out, now, 7).Hit)
	require.True(t, c.Put(k, wire, now))
	for _, dims := range [][3]uint64{{2, 2, 3}, {1, 3, 3}, {1, 2, 4}} {
		key, _ := NewKey(&q, dims[0], dims[1], dims[2])
		assert.False(t, c.Get(key, &q, out, now).Hit)
	}
	qtype := q
	qtype.Question.Type = dns.TypeAAAA
	aaaa, ok := NewKey(&qtype, 1, 2, 3)
	require.True(t, ok)
	assert.False(t, c.Get(aaaa, &qtype, out, now).Hit)
	q.Question.Header.Flags |= dnswire.FlagCD
	cd, _ := NewKey(&q, 1, 2, 3)
	assert.False(t, c.Get(cd, &q, out, now).Hit)
	q.EDNS.Present, q.EDNS.DO = true, true
	q.Question.Header.Additionals = 1
	do, ok := NewKey(&q, 1, 2, 3)
	require.True(t, ok)
	assert.False(t, c.Get(do, &q, out, now).Hit)
}

func TestNegativeCacheAndBypasses(t *testing.T) {
	c, err := New(Config{Bytes: 128 << 10, Shards: 1, MaxNegativeTTL: 45})
	require.NoError(t, err)
	q, wire := fixture(t, "example.org.")
	k, ok := NewKey(&q, 1, 1, 0)
	require.True(t, ok)
	now := time.Now()
	var original dns.Msg
	require.NoError(t, original.Unpack(wire))
	for _, rcode := range []int{dns.RcodeSuccess, dns.RcodeNameError} {
		m := original.Copy()
		m.Rcode, m.Answer = rcode, nil
		soa, err := dns.NewRR("example.org. 120 IN SOA ns.example.org. admin.example.org. 1 2 3 4 90")
		require.NoError(t, err)
		m.Ns = []dns.RR{soa}
		w, err := m.Pack()
		require.NoError(t, err)
		require.True(t, c.Put(k, w, now))
		out := make([]byte, 512)
		r := c.Get(k, &q, out, now.Add(44*time.Second))
		require.True(t, r.Hit)
		assert.True(t, r.Negative)
		var got dns.Msg
		require.NoError(t, got.Unpack(out[:r.Length]))
		assert.Equal(t, rcode, got.Rcode)
		assert.EqualValues(t, 1, got.Ns[0].Header().Ttl)
		assert.False(t, c.Get(k, &q, out, now.Add(45*time.Second)).Hit)
	}
	for _, tc := range []struct {
		name   string
		change func(*dns.Msg)
	}{
		{"TC", func(m *dns.Msg) { m.Truncated = true }},
		{"SERVFAIL", func(m *dns.Msg) { m.Rcode = dns.RcodeServerFailure }},
		{"zeroTTL", func(m *dns.Msg) { m.Answer[0].Header().Ttl = 0 }},
		{"highTTL", func(m *dns.Msg) { m.Answer[0].Header().Ttl = 1 << 31 }},
		{"empty", func(m *dns.Msg) { m.Answer = nil }},
		{"negativeWithoutSOA", func(m *dns.Msg) { m.Rcode = dns.RcodeNameError; m.Answer = nil }},
		{"foreignSOA", func(m *dns.Msg) {
			m.Answer = nil
			m.Ns = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: "other.org.", Rrtype: 6, Class: 1, Ttl: 100}, Ns: ".", Mbox: ".", Minttl: 60}}
		}},
		{"large", func(m *dns.Msg) {
			m.Answer[1] = &dns.RFC3597{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: 65400, Class: 1, Ttl: 100}, Rdata: fmt.Sprintf("%036000x", 1)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := original.Copy()
			tc.change(m)
			w, err := m.Pack()
			require.NoError(t, err)
			assert.False(t, c.Put(k, w, now))
		})
	}
	assert.False(t, c.Put(k, wire[:len(wire)-1], now))
	wrong, _ := fixture(t, "wrong.org.")
	wrongKey, _ := NewKey(&wrong, 1, 1, 0)
	assert.False(t, c.Put(wrongKey, wire, now))
	q.EDNS.Options = []byte{0, 8, 0, 0}
	_, ok = NewKey(&q, 1, 1, 0)
	assert.False(t, ok)
}

func TestCacheEDNSAndOutputBoundaries(t *testing.T) {
	c, err := New(Config{Bytes: 128 << 10, Shards: 1})
	require.NoError(t, err)
	query := new(dns.Msg)
	query.SetQuestion("example.org.", dns.TypeA)
	query.SetEdns0(1232, true)
	query.CheckingDisabled = true
	w, err := query.Pack()
	require.NoError(t, err)
	var q dnswire.Message
	require.NoError(t, dnswire.ParseRequest(w, &q))
	k, ok := NewKey(&q, 1, 1, 0)
	require.True(t, ok)
	m := new(dns.Msg)
	m.SetReply(query)
	m.SetEdns0(1232, true)
	a, err := dns.NewRR("example.org. 60 IN A 192.0.2.1")
	require.NoError(t, err)
	m.Answer = []dns.RR{a}
	w, err = m.Pack()
	require.NoError(t, err)
	now := time.Now()
	require.True(t, c.Put(k, w, now))
	small := bytes.Repeat([]byte{0xcc}, len(w)-1)
	assert.False(t, c.Get(k, &q, small, now).Hit)
	assert.Equal(t, bytes.Repeat([]byte{0xcc}, len(small)), small)
	out := make([]byte, 512)
	r := c.Get(k, &q, out, now.Add(time.Second))
	require.True(t, r.Hit)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:r.Length]))
	assert.True(t, got.CheckingDisabled)
	assert.EqualValues(t, 0x8000, got.IsEdns0().Hdr.Ttl)
	assert.EqualValues(t, 1232, got.IsEdns0().UDPSize())
	assert.False(t, c.Get(k, &q, out, now.Add(-time.Second)).Hit)
	q.EDNS.DO, q.EDNS.Flags = false, 0
	withoutDO, ok := NewKey(&q, 1, 1, 0)
	require.True(t, ok)
	assert.False(t, c.Get(withoutDO, &q, out, now).Hit)
	assert.False(t, c.Put(withoutDO, w, now))
	q.EDNS.DO, q.EDNS.Flags = true, 0x8000
	q.EDNS.UDPSize = 4096
	assert.False(t, c.Get(k, &q, out, now).Hit)
	op := m.IsEdns0()
	op.Option = []dns.EDNS0{&dns.EDNS0_LOCAL{Code: 65000, Data: []byte{1}}}
	w, err = m.Pack()
	require.NoError(t, err)
	assert.False(t, c.Put(k, w, now))
}

func TestCacheBudgetEvictionAndAllocations(t *testing.T) {
	assert.EqualValues(t, 40, unsafe.Sizeof(slot{}))
	for _, budget := range []int{-1, 0, 1024, 1<<30 + 1} {
		_, err := New(Config{Bytes: budget})
		assert.Error(t, err)
	}
	c, err := New(Config{Bytes: 64 << 10, Shards: 1})
	require.NoError(t, err)
	q, w := fixture(t, "example.org.")
	k, _ := NewKey(&q, 1, 1, 0)
	now := time.Now()
	require.True(t, c.Put(k, w, now))
	out := make([]byte, 512)
	var hit CacheResult
	allocs := testing.AllocsPerRun(100, func() { hit = c.Get(k, &q, out, now) })
	require.True(t, hit.Hit)
	assert.Zero(t, allocs)
	var inserted bool
	allocs = testing.AllocsPerRun(100, func() { inserted = c.Put(k, w, now) })
	require.True(t, inserted)
	assert.Zero(t, allocs)
	for i := 0; i < 1000; i++ {
		key, _ := NewKey(&q, 1, uint64(i), 0)
		require.True(t, c.Put(key, w, now))
	}
	stats := c.Stats()
	assert.LessOrEqual(t, stats.RetainedBytes, 64<<10)
	assert.Positive(t, stats.Shards[0].Evictions)
	assert.LessOrEqual(t, stats.Shards[0].OccupiedBytes, stats.Shards[0].ArenaBytes)
	assert.False(t, c.Get(k, &q, out, now).Hit)
	last, _ := NewKey(&q, 1, 999, 0)
	require.True(t, c.Get(last, &q, out, now).Hit)
	// Exact accounting includes all owned backing arrays and fixed objects.
	want := rounded(int(unsafe.Sizeof(*c))) + rounded(len(c.shards)*int(unsafe.Sizeof(shard{})))
	for i := range c.shards {
		s := &c.shards[i]
		want += rounded(cap(s.arena)) + rounded(cap(s.slots)*int(unsafe.Sizeof(slot{}))) + rounded(cap(s.used)*8)
	}
	assert.Equal(t, want, stats.RetainedBytes)
	runtime.GC()
	allocs = testing.AllocsPerRun(100, func() { hit = c.Get(last, &q, out, now) })
	require.True(t, hit.Hit)
	assert.Zero(t, allocs)
}

func TestConcurrentEvictionPoisoning(t *testing.T) {
	c, err := New(Config{Bytes: 128 << 10, Shards: 4})
	require.NoError(t, err)
	q, w := fixture(t, "example.org.")
	now := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			out := make([]byte, 512)
			input := bytes.Clone(w)
			for i := 0; i < 500; i++ {
				k, _ := NewKey(&q, uint64(worker), uint64(i), 0)
				copy(input, w)
				if !c.Put(k, input, now) {
					errs <- fmt.Errorf("put worker %d", worker)
					return
				}
				clear(input)
				r := c.Get(k, &q, out, now.Add(time.Second))
				if !r.Hit {
					continue
				} // another worker may have evicted it
				var got dns.Msg
				if err := got.Unpack(out[:r.Length]); err != nil {
					errs <- err
					return
				}
				if len(got.Answer) != 2 || got.Answer[0].Header().Ttl != 119 || got.Answer[1].(*dns.RFC3597).Rdata != "c001ff00" {
					errs <- fmt.Errorf("poisoned answer: %v", got)
					return
				}
				clear(out)
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
}

func TestArenaFragmentationAndRetainedOutput(t *testing.T) {
	c, err := New(Config{Bytes: 64 << 10, Shards: 1})
	require.NoError(t, err)
	q, wire := fixture(t, "example.org.")
	now := time.Now()
	var m dns.Msg
	require.NoError(t, m.Unpack(wire))
	k, _ := NewKey(&q, 1, 0, 0)
	require.True(t, c.Put(k, wire, now))
	retained := make([]byte, 512)
	r := c.Get(k, &q, retained, now)
	require.True(t, r.Hit)
	expected := bytes.Clone(retained)
	for i := 1; i <= 200; i++ {
		size := []int{1, 300, 900, 3000, 8000}[i%5]
		data := bytes.Repeat([]byte{byte(i)}, size)
		m.Answer[1].(*dns.RFC3597).Rdata = fmt.Sprintf("%x", data)
		w, err := m.Pack()
		require.NoError(t, err)
		key, _ := NewKey(&q, 1, uint64(i), 0)
		require.True(t, c.Put(key, w, now))
		out := make([]byte, MaxMessage)
		r := c.Get(key, &q, out, now)
		require.True(t, r.Hit)
		var got dns.Msg
		require.NoError(t, got.Unpack(out[:r.Length]))
		assert.Equal(t, m.Answer[1].(*dns.RFC3597).Rdata, got.Answer[1].(*dns.RFC3597).Rdata)
		stats := c.Stats()
		assert.LessOrEqual(t, stats.Shards[0].OccupiedBytes, stats.Shards[0].ArenaBytes)
	}
	assert.Equal(t, expected, retained, "eviction must not mutate caller-owned output")
	assert.Positive(t, c.Stats().Shards[0].Evictions)
}

func TestElapsedDoesNotWrap(t *testing.T) {
	seconds, ok := age(1<<63-1, -1<<63)
	assert.True(t, ok)
	assert.EqualValues(t, uint64(^uint64(0))/uint64(time.Second), seconds)
	_, ok = age(-1, 1)
	assert.False(t, ok)
	c, err := New(Config{Bytes: 64 << 10, Shards: 1})
	require.NoError(t, err)
	q, w := fixture(t, "example.org.")
	k, _ := NewKey(&q, 1, 1, 0)
	now := time.Now()
	require.True(t, c.Put(k, w, now))
	out := make([]byte, 512)
	assert.False(t, c.Get(k, &q, out, now.Add(time.Duration(1<<63-1))).Hit)
}

func TestColdAdmissionAllocations(t *testing.T) {
	c, err := New(Config{Bytes: 1 << 20, Shards: 4})
	require.NoError(t, err)
	q, w := fixture(t, "example.org.")
	now := time.Now()
	var generation uint64
	var inserted bool
	allocs := testing.AllocsPerRun(1, func() { generation++; k, _ := NewKey(&q, 1, generation, 0); inserted = c.Put(k, w, now) })
	require.True(t, inserted)
	assert.Zero(t, allocs)
	entries, evictions := 0, 0
	stats := c.Stats()
	for _, s := range stats.Shards[:stats.ShardCount] {
		entries += s.Entries
		evictions += s.Evictions
	}
	assert.Equal(t, 2, entries, "warmup and measured admission both started with absent keys")
	assert.Zero(t, evictions)
}
