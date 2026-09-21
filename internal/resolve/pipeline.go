// Package resolve connects client transport to validated upstream exchanges.
package resolve

import (
	"context"
	"errors"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type Pipeline struct{ upstream *upstream.Client }

func New(c *upstream.Client) *Pipeline { return &Pipeline{upstream: c} }
func (p *Pipeline) Resolve(ctx context.Context, r *transport.Request, out []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	q := &r.Message.Question
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
