// Package resolve connects client transport to validated upstream exchanges.
package resolve

import (
	"context"
	"errors"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type Pipeline struct {
	upstream *upstream.Client
	store    *config.Store
}

func New(c *upstream.Client) *Pipeline { return &Pipeline{upstream: c} }
func NewWithStore(c *upstream.Client, store *config.Store) *Pipeline {
	return &Pipeline{upstream: c, store: store}
}
func (p *Pipeline) Resolve(ctx context.Context, r *transport.Request, out []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	q := &r.Message.Question
	if p.store != nil {
		// One immutable generation for this request. Alias/local-response policy
		// will use this same snapshot when that pipeline is added.
		snapshot := p.store.Snapshot()
		if snapshot == nil {
			return 0, errors.New("resolve: no active policy")
		}
		if n, handled, err := snapshot.Local().Answer(out, &r.Message); handled || err != nil {
			return n, err
		}
		name, err := policy.NameFromWire(q.Name.Canonical[:q.Name.Length])
		if err != nil {
			return 0, err
		}
		if snapshot.Policy().Match(name).Result == policy.Block {
			return dnswire.BuildReply(out, &r.Message, dnswire.Reply{Null: true, TTL: 60, RecursionAvailable: true}, 1232)
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
	return dnswire.PersonalizeReply(out, out[:result.N], &r.Message)
}
