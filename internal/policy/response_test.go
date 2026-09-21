package policy_test

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRelevantAliases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rrs     []string
		extra   []string
		allow   string
		blocked string
		bad     bool
		typ     uint16
	}{
		{name: "cname", rrs: []string{"start.example. 30 IN CNAME ads.example."}, blocked: "ads.example"},
		{name: "dname", rrs: []string{"example. 30 IN DNAME ads.example."}, blocked: "start.ads.example", bad: true},
		{name: "dname target", rrs: []string{"example. 30 IN DNAME blocked.test."}, blocked: "start.blocked.test"},
		{name: "https alias", rrs: []string{"start.example. 30 IN HTTPS 0 ads.example."}, typ: 65, blocked: "ads.example"},
		{name: "https service", rrs: []string{"start.example. 30 IN HTTPS 1 ads.example."}, typ: 65},
		{name: "unrelated additional", extra: []string{"other.example. 30 IN CNAME ads.example."}},
		{name: "unrelated answer", rrs: []string{"other.example. 30 IN CNAME ads.example."}},
		{name: "original allow", rrs: []string{"start.example. 30 IN CNAME ads.example."}, allow: "start.example"},
		{name: "target allow only", rrs: []string{"start.example. 30 IN CNAME exempt.example.", "exempt.example. 30 IN CNAME ads.example."}, allow: "exempt.example", blocked: "ads.example"},
		{name: "loop even allowed", rrs: []string{"start.example. 30 IN CNAME start.example."}, allow: "start.example", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := []policy.Rule{{ID: "deny", Kind: policy.Suffix, Class: policy.CustomDeny, Pattern: "ads.example"}, {ID: "deny2", Kind: policy.Suffix, Class: policy.CustomDeny, Pattern: "blocked.test"}}
			if tc.allow != "" {
				rules = append(rules, policy.Rule{ID: "allow", Kind: policy.Exact, Class: policy.CustomAllow, Pattern: tc.allow})
			}
			p, e := policy.CompileSnapshot(1, rules, policy.DefaultLimits())
			require.NoError(t, e)
			q := new(dns.Msg)
			typ := tc.typ
			if typ == 0 {
				typ = 1
			}
			q.SetQuestion("start.example.", typ)
			m := new(dns.Msg)
			m.SetReply(q)
			for _, text := range tc.rrs {
				r, e := dns.NewRR(text)
				require.NoError(t, e)
				m.Answer = append(m.Answer, r)
			}
			for _, text := range tc.extra {
				r, e := dns.NewRR(text)
				require.NoError(t, e)
				m.Extra = append(m.Extra, r)
			}
			b, e := m.Pack()
			require.NoError(t, e)
			original, e := policy.NormalizeName("start.example")
			require.NoError(t, e)
			result, e := p.InspectResponse(b, original, false)
			if tc.bad {
				assert.Error(t, e)
				return
			}
			require.NoError(t, e)
			assert.Equal(t, tc.blocked, result.BlockedAlias.Display())
		})
	}
}
func TestAliasDepth(t *testing.T) {
	p, e := policy.CompileSnapshot(1, nil, policy.DefaultLimits())
	require.NoError(t, e)
	q := new(dns.Msg)
	q.SetQuestion("n0.example.", 1)
	m := new(dns.Msg)
	m.SetReply(q)
	for i := 0; i < 17; i++ {
		r, e := dns.NewRR(fmt.Sprintf("n%d.example. 30 IN CNAME n%d.example.", i, i+1))
		require.NoError(t, e)
		m.Answer = append(m.Answer, r)
	}
	b, e := m.Pack()
	require.NoError(t, e)
	name, e := policy.NormalizeName("n0.example")
	require.NoError(t, e)
	_, e = p.InspectResponse(b, name, true)
	assert.Error(t, e)
}
func TestResponseActionsAndPause(t *testing.T) {
	now := time.Now()
	s := policy.Settings{PauseUntil: now.Add(time.Minute)}
	assert.True(t, s.Paused(now))
	assert.False(t, s.Paused(now.Add(time.Minute)))
	for _, typ := range []uint16{1, 28, 65} {
		q := new(dns.Msg)
		q.SetQuestion("ads.example.", typ)
		q.AuthenticatedData = true
		q.SetEdns0(1232, true)
		b, e := q.Pack()
		require.NoError(t, e)
		var request dnswire.Message
		require.NoError(t, dnswire.ParseRequest(b, &request))
		out := make([]byte, 65535)
		n, e := policy.BuildBlocked(out, &request, policy.Settings{})
		require.NoError(t, e)
		var got dns.Msg
		require.NoError(t, got.Unpack(out[:n]))
		assert.False(t, got.AuthenticatedData)
		require.NotNil(t, got.IsEdns0())
		require.Len(t, got.IsEdns0().Option, 1)
		assert.EqualValues(t, 15, got.IsEdns0().Option[0].(*dns.EDNS0_EDE).InfoCode)
		if typ == 65 {
			assert.Empty(t, got.Answer)
			require.Len(t, got.Ns, 1)
			assert.EqualValues(t, 2, got.Ns[0].(*dns.SOA).Minttl)
		} else {
			require.Len(t, got.Answer, 1)
		}
	}
}
