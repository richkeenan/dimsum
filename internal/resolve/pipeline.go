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
	upstream *upstream.Client
	store    *config.Store
	names    *clients.Manager
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
	var name policy.Name
	var settings policy.Settings
	paused := false
	if p.store != nil {
		// One immutable generation for this request. Alias/local-response policy
		// will use this same snapshot when that pipeline is added.
		snapshot = p.store.Snapshot()
		if snapshot == nil {
			return 0, errors.New("resolve: no active policy")
		}
		if n, handled, err := snapshot.Local().Answer(out, &r.Message); handled || err != nil {
			return n, err
		}
		var err error
		name, err = policy.NameFromWire(q.Name.Canonical[:q.Name.Length])
		if err != nil {
			return 0, err
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
	if q.Header.Flags&dnswire.FlagRD == 0 {
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 5, RecursionAvailable: true}, 1232)
	}
	if p.upstream == nil {
		return 0, errors.New("resolve: no upstream")
	}
	result, err := p.upstream.Exchange(ctx, r.Wire, out)
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
