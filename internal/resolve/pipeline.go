// Package resolve connects client transport to validated upstream exchanges.
package resolve

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type Pipeline struct {
	upstream        *upstream.Client
	store           *config.Store
	names           *clients.Manager
	upstreams       upstreams
	cache           cacheState
	observeExchange func(upstream.ExchangeResult, error)
}

func New(c *upstream.Client) *Pipeline { return &Pipeline{upstream: c} }

// SetExchangeObserver must be called before concurrent resolution begins. The
// callback must remain bounded and nonblocking; client outcomes are observed by
// transport after fitting, independently of exchanges and background refreshes.
func (p *Pipeline) SetExchangeObserver(fn func(upstream.ExchangeResult, error)) {
	p.observeExchange = fn
}
func NewWithStore(c *upstream.Client, store *config.Store) *Pipeline {
	return &Pipeline{upstream: c, store: store}
}
func NewWithNames(c *upstream.Client, store *config.Store, names *clients.Manager) *Pipeline {
	return &Pipeline{upstream: c, store: store, names: names}
}
func (p *Pipeline) Resolve(ctx context.Context, r *transport.Request, out []byte) (int, error) {
	r.Result = transport.Result{Admitted: r.Result.Admitted}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	q := &r.Message.Question
	r.Result.Outcome = transport.ForwardedAnswer
	if p.names != nil {
		p.names.Observe(r.Peer.Addr())
	}
	var snapshot *config.Snapshot
	name, err := policy.NameFromWire(q.Name.Canonical[:q.Name.Length])
	if err != nil {
		return 0, err
	}
	var settings policy.Settings
	paused := false
	if p.store != nil {
		// One immutable generation for local data, pause, pre- and post-resolution policy.
		snapshot = p.store.Snapshot()
		if snapshot == nil {
			return 0, errors.New("resolve: no active policy")
		}
		r.Result.Generation = snapshot.Generation()
		if !snapshot.Local().Empty() {
			if n, handled, err := snapshot.Local().Answer(out, &r.Message); handled || err != nil {
				r.Result.Outcome = transport.LocalAnswer
				if err == nil && q.Header.Flags&dnswire.FlagRD != 0 {
					if target := snapshot.Local().Continuation(&r.Message); target != nil {
						return p.completeLocal(ctx, r, out, n, target, snapshot)
					}
				}
				return n, err
			}
		}
		settings = snapshot.Filtering()
		paused = settings.Paused(time.Now())
		if policy.PrivateReverse(name) {
			r.Result.Outcome = transport.PolicyBlock
			return policy.BuildBlocked(out, &r.Message, policy.Settings{Mode: "nxdomain"})
		}
		decision, number := snapshot.Policy().EvaluateNumber(policy.Query{Original: name, Name: name, Paused: paused})
		if decision.Result == policy.Block {
			r.Result.Outcome = transport.PolicyBlock
			r.Result.RuleNumber = number
			r.Result.Rule, _ = snapshot.Policy().RuleAt(number)
			return buildBlock(out, &r.Message, settings, decision)
		}
	}
	if policy.PrivateReverse(name) {
		r.Result.Outcome = transport.PolicyBlock
		return policy.BuildBlocked(out, &r.Message, policy.Settings{Mode: "nxdomain"})
	}
	c, cfg, err := p.cacheFor(snapshot)
	if err != nil {
		return 0, err
	}
	k, eligible := cacheKey(r, snapshot)
	if eligible {
		hit := c.Lookup(k, &r.Message, out, time.Now(), staleLimit(cfg), uint32(cfg.StaleTTLSeconds))
		if hit.Hit && (!hit.Stale || cfg.StaleMode == "immediate") {
			r.Result.Outcome = transport.FreshAnswer
			if hit.Stale {
				r.Result.Outcome = transport.StaleAnswer
				p.cache.stale.Add(1)
				p.cache.staleAgeSeconds.Add(hit.StaleAge)
				if q.Header.Flags&dnswire.FlagRD != 0 {
					_, _ = p.shared(ctx, snapshot, c, k, r, true)
				}
			} else {
				p.cache.hits.Add(1)
			}
			return p.finish(r, out, hit.Length, snapshot, name, settings, paused, true)
		}
		p.cache.misses.Add(1)
	} else {
		p.cache.bypasses.Add(1)
	}
	if q.Header.Flags&dnswire.FlagRD == 0 {
		r.Result.Outcome = transport.ResolutionError
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 5, RecursionAvailable: true}, 1232)
	}
	if p.upstream == nil && snapshot == nil {
		return 0, errors.New("resolve: no upstream")
	}
	n := 0
	if eligible {
		wire, exchangeErr := p.shared(ctx, snapshot, c, k, r, false)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if exchangeErr != nil || upstreamFailed(wire) {
			hit := c.Lookup(k, &r.Message, out, time.Now(), staleLimit(cfg), uint32(cfg.StaleTTLSeconds))
			if hit.Hit && hit.Stale {
				r.Result.Outcome = transport.StaleAnswer
				p.cache.stale.Add(1)
				p.cache.staleAgeSeconds.Add(hit.StaleAge)
				return p.finish(r, out, hit.Length, snapshot, name, settings, paused, true)
			}
		}
		if exchangeErr != nil {
			return 0, exchangeErr
		}
		if len(wire) > len(out) {
			return 0, errors.New("resolve: output too small")
		}
		n = copy(out, wire)
	} else {
		result, err := p.exchange(ctx, snapshot, upstream.DefaultRoute, r.Wire, out)
		if err != nil {
			return 0, err
		}
		n = result.N
		r.Result.UpstreamID = result.EndpointID
		r.Result.Fallback = result.Fallback
	}
	return p.finish(r, out, n, snapshot, name, settings, paused, false)
}

func (p *Pipeline) finish(r *transport.Request, out []byte, n int, snapshot *config.Snapshot, name policy.Name, settings policy.Settings, paused, personalized bool) (int, error) {
	if n >= 12 && (out[3]&15 == 2 || out[3]&15 == 5) {
		r.Result.Outcome = transport.ResolutionError
	}
	if snapshot != nil {
		decision, err := snapshot.Policy().InspectResponse(out[:n], name, paused)
		if err != nil {
			r.Result.Outcome = transport.ResolutionError
			return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 2, RecursionAvailable: true}, 1232)
		}
		if decision.Decision.Result == policy.Block {
			r.Result.Outcome = transport.PolicyBlock
			r.Result.ResponsePolicy = true
			r.Result.RuleNumber = decision.RuleNumber
			r.Result.Rule, _ = snapshot.Policy().RuleAt(decision.RuleNumber)
			r.Result.AliasLength = uint8(decision.BlockedAlias.CopyWire(r.Result.Alias[:]))
			return buildBlock(out, &r.Message, settings, decision.Decision)
		}
	}
	// Cache templates already validated compression safety and reconstructed ID,
	// question case, flags and TTLs into this client's output. Policy inspection
	// above still runs; the uncached arbitrary-wire relocation path is redundant.
	if personalized {
		return n, nil
	}
	return dnswire.PersonalizeReply(out, out[:n], &r.Message)
}

func buildBlock(out []byte, q *dnswire.Message, s policy.Settings, d policy.Decision) (int, error) {
	if strings.HasPrefix(d.RuleID, "special:") {
		s.Mode = "nxdomain"
		if d.RuleID == "special:_dns.resolver.arpa" {
			s.Mode = "nodata"
		}
	}
	return policy.BuildBlocked(out, q, s)
}
