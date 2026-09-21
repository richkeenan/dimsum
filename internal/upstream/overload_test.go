package upstream

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIDExhaustionDoesNotPenalizeEndpoints(t *testing.T) {
	for _, halfOpen := range []bool{false, true} {
		for _, tcpRetry := range []bool{false, true} {
			for _, quarantine := range []bool{false, true} {
				t.Run(fmt.Sprintf("half-open=%t/tcp-retry=%t/quarantine=%t", halfOpen, tcpRetry, quarantine), func(t *testing.T) {
					handler := func(r testutil.Request) testutil.Response {
						p := append([]byte(nil), r.Wire...)
						p[2] |= 0x80
						if tcpRetry && r.Network == "udp" {
							p[2] |= 2
						}
						return testutil.Response{Wire: p}
					}
					primary, err := testutil.NewUpstream(testutil.NewClock(time.Now()), handler)
					require.NoError(t, err)
					defer primary.Close()
					fallback, err := testutil.NewUpstream(testutil.NewClock(time.Now()), handler)
					require.NoError(t, err)
					defer fallback.Close()
					c, err := New(Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(primary.Address())}, Fallback: []netip.AddrPort{netip.MustParseAddrPort(fallback.Address())}, FailureThreshold: 1})
					require.NoError(t, err)
					defer c.Close()
					if halfOpen {
						c.health[0].RetryAt = time.Now().Add(-time.Second)
						c.health[0].backoff = 5 * time.Second
						c.health[0].consecutive = 1
					}
					before := c.Health()
					c.ids.mu.Lock()
					for i := range c.ids.active {
						if quarantine {
							c.ids.until[i] = time.Now().Add(time.Hour)
						} else {
							c.ids.active[i] = true
						}
					}
					if tcpRetry { // UDP consumes the last ID; TCP must fail locally.
						c.ids.active[42] = false
						c.ids.until[42] = time.Time{}
					}
					c.ids.mu.Unlock()
					wire := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
					out := bytes.Repeat([]byte{0xcc}, 65535)
					r, err := c.Exchange(context.Background(), wire, out)
					assert.ErrorIs(t, err, ErrOverloaded)
					wantAttempts := 0
					if tcpRetry {
						wantAttempts = 1
					}
					assert.Equal(t, wantAttempts, r.Attempts)
					assert.True(t, slices.Equal(before, c.Health()), "local admission failure changed health: before=%+v after=%+v", before, c.Health())
					assert.False(t, c.health[0].probe, "half-open lease must be released")
					assert.Equal(t, uint64(0), c.health[1].epoch, "fallback must not be claimed")
					assert.Len(t, primary.Requests(), wantAttempts)
					assert.Empty(t, fallback.Requests())
					assert.Equal(t, bytes.Repeat([]byte{0xcc}, 65535), out)
					assert.Zero(t, c.Outstanding())
					// Release the synthetic occupancy; recovery needs no circuit delay.
					c.ids.mu.Lock()
					clear(c.ids.active[:])
					clear(c.ids.until[:])
					c.ids.mu.Unlock()
					r, err = c.Exchange(context.Background(), wire, out)
					require.NoError(t, err)
					assert.Equal(t, primary.Address(), r.Endpoint.String())
					assert.Equal(t, "closed", c.Health()[0].State)
					assert.Equal(t, uint64(1), c.Health()[0].Responses)
					assert.Zero(t, c.Health()[0].Failures)
					assert.Empty(t, fallback.Requests())
				})
			}
		}
	}
}
