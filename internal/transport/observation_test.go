package transport

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObserverFinalOutcomes(t *testing.T) {
	query := []byte{0, 42, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
	for _, tc := range []struct {
		name     string
		wire     []byte
		fail     bool
		admitted bool
		outcome  Outcome
		rcode    uint16
	}{
		{"cache", query, false, true, FreshAnswer, 0},
		{"error", query, true, true, ResolutionError, 2},
		{"malformed", query[:8], false, false, ResolutionError, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var results []Result
			var response []byte
			s, err := New(Options{SmallSlots: 1, LargeSlots: 1, Observe: func(r *Request, result Result) {
				response = append([]byte(nil), result.Response...)
				results = append(results, result)
			}}, HandlerFunc(func(ctx context.Context, r *Request, out []byte) (int, error) {
				r.Result.Outcome = FreshAnswer
				if tc.fail {
					return 0, errors.New("fixture failure")
				}
				return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RecursionAvailable: true}, 1232)
			}))
			require.NoError(t, err)
			out := make([]byte, 65535)
			n := s.resolve(context.Background(), tc.wire, out, netip.MustParseAddrPort("192.0.2.1:1234"), false, time.Now().Add(2*time.Second))
			if n > 0 {
				assert.Equal(t, out[:n], response)
			} else {
				assert.Empty(t, response)
			}
			require.Len(t, results, 1)
			assert.Equal(t, tc.admitted, results[0].Admitted)
			assert.Equal(t, tc.outcome, results[0].Outcome)
			assert.Equal(t, tc.rcode, results[0].RCode)
			assert.GreaterOrEqual(t, results[0].Elapsed, time.Duration(0))
		})
	}
}
