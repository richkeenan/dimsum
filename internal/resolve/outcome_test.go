package resolve

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardedErrorIsSingleErrorOutcome(t *testing.T) {
	for _, code := range []uint16{2, 5} {
		wire := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
		r := transport.Request{Wire: wire, Result: transport.Result{Outcome: transport.ForwardedAnswer}}
		require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
		out := make([]byte, 1232)
		n, err := dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: code, RecursionAvailable: true}, 1232)
		require.NoError(t, err)
		_, err = New(nil).finish(&r, out, n, nil, policy.Name{}, policy.Settings{}, false, false)
		require.NoError(t, err)
		assert.Equal(t, transport.ResolutionError, r.Result.Outcome)
	}
}
