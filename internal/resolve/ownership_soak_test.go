package resolve

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep this local and bounded: four delayed exchanges, 48 clients and 8192
// cache insertions. The clock gate makes cancellation and eviction overlap real
// in-flight requests without depending on scheduler-sensitive sleeps.
func TestOwnershipRaceSoak(t *testing.T) {
	const rounds, clients, canceled = 4, 12, 3
	clock := testutil.NewClock(time.Now())
	var exchanges atomic.Int32
	u, err := testutil.NewUpstream(clock, func(req testutil.Request) testutil.Response {
		exchanges.Add(1)
		wire := append([]byte(nil), req.Wire...)
		wire[2] |= 0x80
		wire[7] = 1
		wire = append(wire, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 20)
		return testutil.Response{Wire: wire, Delay: time.Second}
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, u.Close()) })
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [%s]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: %s/data\n  secrets_dir: %s/secrets\ncache:\n  bytes: 524288\n  shards: 4\n  stale_mode: off\n", u.Address(), dir, dir)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	store, err := config.OpenStore(context.Background(), path, filepath.Join(dir, "state"), config.StoreOptions{})
	require.NoError(t, err)
	p := NewWithStore(nil, store)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	c, _, err := p.cacheFor(store.Snapshot())
	require.NoError(t, err)
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	var workers sync.WaitGroup
	defer func() { stop(); workers.Wait() }()

	request := func(name string, id uint16) *transport.Request {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		q.Id = id
		wire, err := q.Pack()
		require.NoError(t, err)
		r := &transport.Request{Wire: wire}
		require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
		return r
	}
	seed := request("Arena.example.", 500)
	seedWire := append([]byte(nil), seed.Wire...)
	seedWire[2] |= 0x80
	seedWire[7] = 1
	seedWire = append(seedWire, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 20)
	key, ok := cacheKey(seed, store.Snapshot())
	require.True(t, ok)
	require.True(t, c.Put(key, seedWire, time.Now()))

	// Retain both a raw cache lookup and a pipeline cache-hit output through all
	// arena recycling. Neither may borrow cache storage or the request wire.
	held := make([]byte, 512)
	hit := c.Lookup(key, &seed.Message, held, time.Now(), 0, 0)
	require.True(t, hit.Hit)
	held = held[:hit.Length]
	heldCopy := bytes.Clone(held)
	cached := make([]byte, 512)
	n, err := p.Resolve(ctx, seed, cached)
	require.NoError(t, err)
	cached = cached[:n]
	cachedCopy := bytes.Clone(cached)
	require.EqualValues(t, 1, p.CacheStats().Hits)
	// Save the parsed message for namespace-key generation before recycling.
	seedMessage := seed.Message
	for i := range seed.Wire {
		seed.Wire[i] = 0xa5
	}
	seed.Message = dnswire.Message{}

	evictions := func() int {
		stats := c.Stats()
		n := 0
		for _, shard := range stats.Shards {
			n += shard.Evictions
		}
		return n
	}
	type result struct {
		id   uint16
		name string
		wire []byte
		err  error
	}
	var retained []result
	for round := range rounds {
		results := make(chan result, clients)
		cancels := make([]context.CancelFunc, clients)
		for client := range clients {
			name := fmt.Sprintf("SoAk%d.ExAmPlE.", round)
			if client%2 == 0 {
				name = fmt.Sprintf("sOaK%d.eXaMpLe.", round)
			}
			id := uint16(round*clients + client + 1)
			r := request(name, id)
			clientCtx, cancel := context.WithCancel(ctx)
			cancels[client] = cancel
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer cancel()
				out := make([]byte, 512)
				n, err := p.Resolve(clientCtx, r, out)
				// Model transport pooling immediately on return, including canceled
				// waiters while the shared exchange still owns its request copy.
				for i := range r.Wire {
					r.Wire[i] = byte(id)
				}
				r.Message = dnswire.Message{}
				results <- result{id, name, out[:n], err}
			}()
		}
		require.Eventually(t, func() bool {
			p.cache.flights.mu.Lock()
			defer p.cache.flights.mu.Unlock()
			for _, f := range p.cache.flights.active {
				if f.waiters == clients {
					return true
				}
			}
			return false
		}, time.Second, time.Millisecond)
		// Consume every fixture notification (also prevents the 64-slot queue
		// becoming a hidden limit if the bounded round count is adjusted).
		select {
		case <-u.Requests():
		case <-ctx.Done():
			t.Fatal("upstream did not register delayed response")
		}
		for _, cancel := range cancels[:canceled] {
			cancel()
		}
		for range canceled {
			select {
			case got := <-results:
				require.ErrorIs(t, got.err, context.Canceled)
				assert.Empty(t, got.wire)
			case <-ctx.Done():
				t.Fatal("canceled waiter did not return")
			}
		}

		before := evictions()
		halfway := make(chan struct{})
		continueChurn := make(chan struct{})
		churnDone := make(chan error, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range 2048 {
				if i == 1024 {
					close(halfway)
					select {
					case <-continueChurn:
					case <-ctx.Done():
						churnDone <- ctx.Err()
						return
					}
				}
				k, ok := dnscache.NewKey(&seedMessage, uint64(100+round*2048+i), 0, 0)
				if !ok || !c.Put(k, seedWire, time.Now()) {
					churnDone <- fmt.Errorf("round %d insertion %d rejected", round, i)
					return
				}
			}
			churnDone <- nil
		}()
		select {
		case <-halfway:
		case err := <-churnDone:
			t.Fatalf("churn stopped before eviction checkpoint: %v", err)
		case <-ctx.Done():
			t.Fatal("cache churn timed out")
		}
		assert.Greater(t, evictions(), before, "evict while successful waiters are blocked upstream")
		clock.Advance(time.Second)
		close(continueChurn)
		for range clients - canceled {
			select {
			case got := <-results:
				require.NoError(t, got.err)
				retained = append(retained, got)
			case <-ctx.Done():
				t.Fatal("successful waiter did not return")
			}
		}
		require.NoError(t, <-churnDone)
	}

	assert.Positive(t, evictions())
	assert.Equal(t, heldCopy, held, "cache lookup output survived arena reuse")
	assert.Equal(t, cachedCopy, cached, "pipeline cache-hit output survived arena reuse")
	for _, got := range retained {
		var m dns.Msg
		require.NoError(t, m.Unpack(got.wire))
		assert.Equal(t, got.id, m.Id)
		require.Len(t, m.Question, 1)
		assert.Equal(t, got.name, m.Question[0].Name)
		assert.Equal(t, dns.RcodeSuccess, m.Rcode)
		require.Len(t, m.Answer, 1)
		a, ok := m.Answer[0].(*dns.A)
		require.True(t, ok)
		assert.Equal(t, "192.0.2.20", a.A.String())
	}
	assert.EqualValues(t, rounds*clients, p.CacheStats().Misses)
	assert.EqualValues(t, rounds, exchanges.Load(), "one upstream exchange per coalesced round")
	assert.Empty(t, u.Requests(), "one upstream exchange per coalesced round")
}
