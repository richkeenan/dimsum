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
	upstream  *upstream.Client
	store     *config.Store
	names     *clients.Manager
	upstreams upstreams
}

func New(c *upstream.Client) *Pipeline { return &Pipeline{upstream: c} }
func NewWithStore(c *upstream.Client, store *config.Store) *Pipeline {
	return &Pipeline{upstream: c, store: store}
}
func NewWithNames(c *upstream.Client, store *config.Store, names *clients.Manager) *Pipeline {
	return &Pipeline{upstream: c, store: store, names: names}
}
func (p *Pipeline) Resolve(ctx context.Context, r *transport.Request, out []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	q := &r.Message.Question
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
		if n, handled, err := snapshot.Local().Answer(out, &r.Message); handled || err != nil {
			if err == nil && q.Header.Flags&dnswire.FlagRD != 0 {
				if target := snapshot.Local().Continuation(&r.Message); target != nil {
					return p.completeLocal(ctx, r, out, n, target, snapshot)
				}
			}
			return n, err
		}
		settings = snapshot.Filtering()
		paused = settings.Paused(time.Now())
		if policy.PrivateReverse(name) {
			return policy.BuildBlocked(out, &r.Message, policy.Settings{Mode: "nxdomain"})
		}
		decision := snapshot.Policy().Evaluate(policy.Query{Original: name, Name: name, Paused: paused})
		if decision.Result == policy.Block {
			return buildBlock(out, &r.Message, settings, decision)
		}
	}
	if policy.PrivateReverse(name) {
		return policy.BuildBlocked(out, &r.Message, policy.Settings{Mode: "nxdomain"})
	}
	if q.Header.Flags&dnswire.FlagRD == 0 {
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 5, RecursionAvailable: true}, 1232)
	}
	if p.upstream == nil && snapshot == nil {
		return 0, errors.New("resolve: no upstream")
	}
	result, err := p.exchange(ctx, snapshot, upstream.DefaultRoute, r.Wire, out)
	if err != nil {
		return 0, err
	}
	if snapshot != nil {
		decision, err := snapshot.Policy().InspectResponse(out[:result.N], name, paused)
		if err != nil {
			return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 2, RecursionAvailable: true}, 1232)
		}
		if decision.Decision.Result == policy.Block {
			return buildBlock(out, &r.Message, settings, decision.Decision)
		}
	}
	return dnswire.PersonalizeReply(out, out[:result.N], &r.Message)
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
