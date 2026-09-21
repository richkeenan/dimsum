package resolve

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type cacheState struct {
	once                                                              sync.Once
	cache                                                             *dnscache.Cache
	err                                                               error
	flights                                                           flights
	hits, misses, bypasses, stale, refreshOK, refreshFailed, overflow atomic.Uint64
	staleAgeSeconds                                                   atomic.Uint64
}

type CacheStats struct{ Hits, Misses, Bypasses, Stale, RefreshSuccess, RefreshFailure, Overflow, StaleAgeSeconds uint64 }

// Cache defaults contain no slices; avoid constructing the full configuration
// (including its listener slice) for each request.
var defaultCacheSettings = config.Default().Cache

func (p *Pipeline) CacheStats() CacheStats {
	c := &p.cache
	return CacheStats{c.hits.Load(), c.misses.Load(), c.bypasses.Load(), c.stale.Load(), c.refreshOK.Load(), c.refreshFailed.Load(), c.overflow.Load(), c.staleAgeSeconds.Load()}
}

// One allocation budget per pipeline lifetime. Allocation settings require a
// restart; generations share storage but never keys, so reload cannot accumulate
// overlapping cache instances. Old completions remain in their original namespace.
func (p *Pipeline) cacheFor(s *config.Snapshot) (*dnscache.Cache, config.Cache, error) {
	cfg := defaultCacheSettings
	if s != nil {
		cfg = s.CacheSettings()
	}
	p.cache.once.Do(func() {
		p.cache.cache, p.cache.err = dnscache.New(dnscache.Config{Bytes: cfg.Bytes, Shards: cfg.Shards, MaxNegativeTTL: uint32(cfg.MaxNegativeTTLSeconds)})
	})
	return p.cache.cache, cfg, p.cache.err
}

func cacheKey(r *transport.Request, s *config.Snapshot) (dnscache.Key, bool) {
	// Forwarding normalizes EDNS to 1232. Preserve OPT only when that is
	// already the client's variant; options and other shapes bypass NewKey.
	if r.Message.EDNS.Present && r.Message.EDNS.UDPSize != 1232 {
		return dnscache.Key{}, false
	}
	generation := uint64(0)
	if s != nil {
		generation = s.Generation()
	}
	return dnscache.NewKey(&r.Message, uint64(upstream.DefaultRoute), generation, 0)
}

func (p *Pipeline) shared(ctx context.Context, s *config.Snapshot, c *dnscache.Cache, k dnscache.Key, r *transport.Request, refresh bool) ([]byte, error) {
	// Copy before returning to a stale client or sharing work past its deadline.
	wire := append([]byte(nil), r.Wire...)
	var metadata upstream.ExchangeResult
	f, joined, err := p.cache.flights.joinStatus(k, refresh, func(workCtx context.Context) ([]byte, error) {
		if refresh {
			workCtx = context.WithValue(workCtx, backgroundExchangeKey{}, true)
		}
		out := make([]byte, 65535)
		result, err := p.exchange(workCtx, s, upstream.DefaultRoute, wire, out)
		metadata = result
		if err == nil {
			out = out[:result.N]
			// Only authenticated upstream originals enter the cache, before policy
			// synthesis or per-client personalization.
			c.Put(k, out, time.Now())
		}
		if refresh {
			if err == nil && len(out) >= 4 && out[3]&15 == 0 {
				p.cache.refreshOK.Add(1)
			} else {
				p.cache.refreshFailed.Add(1)
			}
		}
		return out, err
	}, &metadata)
	if err != nil {
		p.cache.overflow.Add(1)
		return nil, err
	}
	if refresh {
		return nil, nil
	}
	r.Result.Coalesced = joined
	response, err := p.cache.flights.wait(ctx, k, f)
	if err == nil && f.metadata != nil {
		r.Result.UpstreamID = f.metadata.EndpointID
		r.Result.Fallback = f.metadata.Fallback
	}
	return response, err
}

func staleLimit(cfg config.Cache) uint32 {
	if cfg.StaleMode == "off" {
		return 0
	}
	return uint32(cfg.MaxStaleSeconds)
}

func upstreamFailed(wire []byte) bool {
	return len(wire) >= 12 && wire[3]&15 == 2
}
