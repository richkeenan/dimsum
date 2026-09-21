// Package resolve connects client transport to validated upstream exchanges.
package resolve

import (
	"context"
	"encoding/binary"
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
	binary.BigEndian.PutUint16(out, q.Header.ID)
	flags := binary.BigEndian.Uint16(out[2:4]) &^ (dnswire.FlagAD | dnswire.FlagRD | dnswire.FlagCD)
	flags |= q.Header.Flags & (dnswire.FlagRD | dnswire.FlagCD)
	binary.BigEndian.PutUint16(out[2:4], flags)
	// Validation guarantees the upstream question is expanded and the same length;
	// patching case never shifts opaque RDATA or invalidates compression offsets.
	copy(out[12:12+int(q.Name.Length)], q.Name.Wire[:q.Name.Length])
	return result.N, nil
}
