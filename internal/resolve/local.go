package resolve

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

// completeLocal follows an external target under the same captured generation.
// Local routing wins over name filtering, but combined-chain validation still
// applies. No upstream signature or AD assertion survives the synthesis.
func (p *Pipeline) completeLocal(ctx context.Context, r *transport.Request, out []byte, n int, target []byte, snapshot *config.Snapshot) (int, error) {
	answers, _, err := dnswire.SyntheticSections(out[:n])
	if err != nil {
		return 0, err
	}
	targetName, err := policy.NameFromWire(target)
	if err != nil {
		return 0, err
	}
	var authority []dnswire.SyntheticRecord
	var code uint16
	if policy.PrivateReverse(targetName) {
		code = 3
		authority = []dnswire.SyntheticRecord{dnswire.NegativeSOA([]byte{0}, 2)}
	} else {
		if p.upstream == nil && snapshot == nil {
			return 0, errors.New("resolve: no upstream for local alias target")
		}
		h := r.Message.Question.Header
		expected := uint16(0)
		if r.Message.EDNS.Present {
			expected = 1
		}
		if h.Answers != 0 || h.Authorities != 0 || h.Additionals != expected {
			return 0, dnswire.ErrUnsupported
		}
		query := make([]byte, 12)
		binary.BigEndian.PutUint16(query[2:], h.Flags&(dnswire.FlagRD|dnswire.FlagCD))
		query[5] = 1
		query = append(query, target...)
		query = binary.BigEndian.AppendUint16(query, r.Message.Question.Type)
		query = binary.BigEndian.AppendUint16(query, r.Message.Question.Class)
		if r.Message.EDNS.Present {
			query[11] = 1
			query = append(query, 0, 0, 41, 4, 208, 0, 0, 0, 0, 0, 0)
			if r.Message.EDNS.DO {
				query[len(query)-4] = 0x80
			}
			binary.BigEndian.PutUint16(query[len(query)-2:], uint16(len(r.Message.EDNS.Options)))
			query = append(query, r.Message.EDNS.Options...)
		}
		result, e := p.exchange(ctx, snapshot, upstream.DefaultRoute, query, out)
		if e != nil {
			return 0, e
		}
		r.Result.UpstreamID = result.EndpointID
		r.Result.Fallback = result.Fallback
		var message dnswire.Message
		if e = dnswire.ScanMessage(out[:result.N], &message); e != nil {
			return 0, e
		}
		code = message.RCode
		if code == 2 || code == 5 {
			r.Result.Outcome = transport.ResolutionError
		}
		more, ns, e := dnswire.SyntheticSections(out[:result.N])
		if e != nil {
			return 0, e
		}
		answers = append(answers, more...)
		authority = ns
	}
	n, err = dnswire.BuildSynthetic(out, &r.Message, code, false, answers, authority)
	if err != nil {
		return 0, err
	}
	original, err := policy.NameFromWire(r.Message.Question.Name.Canonical[:r.Message.Question.Name.Length])
	if err != nil {
		return 0, err
	}
	if _, err = snapshot.Policy().InspectResponse(out[:n], original, true); err != nil {
		r.Result.Outcome = transport.ResolutionError
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 2, RecursionAvailable: true}, 1232)
	}
	return n, nil
}
