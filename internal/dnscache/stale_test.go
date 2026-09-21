package dnscache

import (
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaleLifetimeAndTTL(t *testing.T) {
	c, err := New(Config{Bytes: 1 << 20})
	require.NoError(t, err)
	q, wire := fixture(t, "example.org.")
	k, ok := NewKey(&q, 1, 1, 0)
	require.True(t, ok)
	now := time.Now()
	require.True(t, c.Put(k, wire, now))
	out := make([]byte, 512)
	for _, seconds := range []int{60, 65, 3659} {
		r := c.Lookup(k, &q, out, now.Add(time.Duration(seconds)*time.Second), 3600, 30)
		require.True(t, r.Hit)
		assert.True(t, r.Stale)
		assert.EqualValues(t, seconds-60, r.StaleAge)
		var m dns.Msg
		require.NoError(t, m.Unpack(out[:r.Length]))
		assert.False(t, m.AuthenticatedData)
		for _, rr := range m.Answer {
			assert.EqualValues(t, 30, rr.Header().Ttl)
		}
	}
	assert.False(t, c.Lookup(k, &q, out, now.Add(3660*time.Second), 3600, 30).Hit)
}

func TestNegativeNeverStale(t *testing.T) {
	c, err := New(Config{Bytes: 1 << 20})
	require.NoError(t, err)
	q, wire := fixture(t, "example.org.")
	k, _ := NewKey(&q, 1, 1, 0)
	var m dns.Msg
	require.NoError(t, m.Unpack(wire))
	m.Answer = nil
	m.Rcode = dns.RcodeNameError
	soa, err := dns.NewRR("example.org. 60 IN SOA ns.example.org. admin.example.org. 1 2 3 4 60")
	require.NoError(t, err)
	m.Ns = []dns.RR{soa}
	wire, err = m.Pack()
	require.NoError(t, err)
	now := time.Now()
	require.True(t, c.Put(k, wire, now))
	out := make([]byte, 512)
	assert.True(t, c.Lookup(k, &q, out, now.Add(59*time.Second), 3600, 30).Hit)
	assert.False(t, c.Lookup(k, &q, out, now.Add(60*time.Second), 3600, 30).Hit)
}
