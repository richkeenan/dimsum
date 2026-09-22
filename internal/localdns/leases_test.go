package localdns_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDHCPDomainExplicitEmptyNonterminalsAndAliases(t *testing.T) {
	now := time.Now()
	z, err := localdns.Build(nil, []localdns.Record{
		{Name: "printer.room.home.arpa", Type: "A", Value: "192.0.2.9", TTL: 60},
		{Name: "to-room.test", Type: "CNAME", Value: "room.home.arpa", TTL: 60},
		{Name: "chain.test", Type: "CNAME", Value: "to-room.test", TTL: 60},
		{Name: "to-missing.test", Type: "CNAME", Value: "missing.home.arpa", TTL: 60},
	})
	require.NoError(t, err)
	v := localdns.BuildLeases(1, "home.arpa", nil, nil, z)
	for _, tc := range []struct {
		name          string
		typ           uint16
		code, answers int
	}{
		{"room.home.arpa.", dns.TypeA, 0, 0},
		{"room.home.arpa.", dns.TypeAAAA, 0, 0},
		{"room.home.arpa.", dns.TypeSOA, 0, 0},
		{"to-room.test.", dns.TypeA, 0, 1},
		{"to-room.test.", dns.TypeAAAA, 0, 1},
		{"to-room.test.", dns.TypeSOA, 0, 1},
		{"chain.test.", dns.TypeA, 0, 2},
		{"missing.home.arpa.", dns.TypeA, 3, 0},
		{"child.room.home.arpa.", dns.TypeA, 3, 0},
		{"to-missing.test.", dns.TypeA, 3, 1},
	} {
		t.Run(tc.name+dns.TypeToString[tc.typ], func(t *testing.T) {
			q := new(dns.Msg)
			q.SetQuestion(tc.name, tc.typ)
			b, e := q.Pack()
			require.NoError(t, e)
			var m dnswire.Message
			require.NoError(t, dnswire.ParseRequest(b, &m))
			out := make([]byte, 1232)
			n, ok, e := z.AnswerWithLeases(out, &m, v, now)
			require.NoError(t, e)
			require.True(t, ok)
			var got dns.Msg
			require.NoError(t, got.Unpack(out[:n]))
			assert.Equal(t, tc.code, got.Rcode)
			require.Len(t, got.Answer, tc.answers)
			require.Len(t, got.Ns, 1)
			assert.Equal(t, "home.arpa.", got.Ns[0].Header().Name)
			assert.EqualValues(t, 30, got.Ns[0].Header().Ttl)
			assert.Nil(t, z.ContinuationWithLeases(&m, v, now), "terminal local negatives must not trigger upstream alias completion")
			if tc.answers == 0 {
				// Prepared existence indexes also preserve direct-path allocation goals.
				allocs := testing.AllocsPerRun(100, func() { n, ok, e = v.Answer(out, &m, now) })
				require.NoError(t, e)
				require.True(t, ok)
				assert.Zero(t, allocs)
				require.NoError(t, got.Unpack(out[:n]))
				assert.Equal(t, tc.code, got.Rcode)
				assert.Empty(t, got.Answer)
			}
		})
	}
}

func TestLeaseAnswersPrecedenceAndExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	z, err := localdns.Build([]localdns.Zone{{Name: "home.arpa", NegativeTTL: 42}}, []localdns.Record{
		{Name: "fixed.home.arpa", Type: "A", Value: "192.0.2.9", TTL: 60},
		{Name: "alias.home.arpa", Type: "CNAME", Value: "desk.home.arpa", TTL: 60},
		{Name: "outside.test", Type: "CNAME", Value: "desk.home.arpa", TTL: 60},
	})
	require.NoError(t, err)
	rows := []localdns.Lease{{Address: netip.MustParseAddr("192.0.2.100"), Hostname: "DESK", Expiry: now.Add(2500 * time.Millisecond)},
		{Address: netip.MustParseAddr("192.0.2.101"), Hostname: "fixed", Expiry: now.Add(time.Minute)}}
	v := localdns.BuildLeases(7, "home.arpa", rows, nil, z)
	for _, tc := range []struct {
		name          string
		typ           uint16
		at            time.Time
		code, answers int
		ttl           uint32
	}{
		{"desk.home.arpa.", 1, now, 0, 1, 2},
		{"100.2.0.192.in-addr.arpa.", 12, now, 0, 1, 2},
		{"desk.home.arpa.", 28, now, 0, 0, 2},
		{"missing.home.arpa.", 1, now, 3, 0, 42},
		{"alias.home.arpa.", 1, now, 0, 2, 2},
		{"alias.home.arpa.", 28, now, 0, 1, 60},
		{"outside.test.", 1, now, 0, 2, 2},
		{"home.arpa.", 6, now, 0, 1, 42},
		{"fixed.home.arpa.", 1, now, 0, 1, 60},
		{"host-192-0-2-101.home.arpa.", 1, now, 0, 1, 30},
		{"desk.home.arpa.", 1, now.Add(2 * time.Second), 0, 1, 0},
		{"desk.home.arpa.", 1, now.Add(2500 * time.Millisecond), 3, 0, 42},
		{"alias.home.arpa.", 1, now.Add(3 * time.Second), 3, 1, 60},
	} {
		t.Run(tc.name+dns.TypeToString[tc.typ]+tc.at.String(), func(t *testing.T) {
			q := new(dns.Msg)
			q.SetQuestion(tc.name, tc.typ)
			b, e := q.Pack()
			require.NoError(t, e)
			var m dnswire.Message
			require.NoError(t, dnswire.ParseRequest(b, &m))
			out := make([]byte, 1232)
			n, ok, e := z.AnswerWithLeases(out, &m, v, tc.at)
			require.NoError(t, e)
			require.True(t, ok)
			var got dns.Msg
			require.NoError(t, got.Unpack(out[:n]))
			assert.Equal(t, tc.code, got.Rcode)
			require.Len(t, got.Answer, tc.answers)
			if tc.answers > 0 {
				assert.Equal(t, tc.ttl, got.Answer[tc.answers-1].Header().Ttl)
			} else {
				require.Len(t, got.Ns, 1)
				assert.Equal(t, tc.ttl, got.Ns[0].Header().Ttl)
			}
			if tc.typ == 28 {
				require.Len(t, got.Ns, 1)
				assert.EqualValues(t, 2, got.Ns[0].Header().Ttl)
			}
		})
	}
	assert.Equal(t, "desk.home.arpa", v.Name(rows[0].Address, now).Hostname)
	assert.Empty(t, v.Name(rows[0].Address, now.Add(3*time.Second)).Hostname)
	// Input ownership must not leak into the immutable publication.
	rows[0].Hostname = "mutated"
	assert.Equal(t, "desk.home.arpa", v.Name(rows[0].Address, now).Hostname)
}

func TestLeaseLabelsReserveConfiguredAndGeneratedNames(t *testing.T) {
	now := time.Now()
	ip := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	rows := []localdns.Lease{
		{Address: ip("192.0.2.100"), Hostname: "same", Expiry: now.Add(time.Minute)},
		{Address: ip("192.0.2.101"), Hostname: "same", Expiry: now.Add(time.Minute)},
		{Address: ip("192.0.2.102"), Hostname: "bad.label", Expiry: now.Add(time.Minute)},
		{Address: ip("192.0.2.103"), Hostname: "reserved", Expiry: now.Add(time.Minute)},
		{Address: ip("192.0.2.104"), Hostname: "host-192-0-2-102", Expiry: now.Add(time.Minute)},
		{Address: ip("192.0.2.105"), Hostname: "host-office", Expiry: now.Add(time.Minute)},
		{Address: ip("192.0.2.20"), Hostname: "ignored", Expiry: now.Add(time.Minute)},
	}
	z, e := localdns.Build(nil, nil)
	require.NoError(t, e)
	v := localdns.BuildLeases(1, "home.arpa", rows, map[netip.Addr]string{ip("192.0.2.20"): "reserved"}, z)
	for i, want := range []string{"host-192-0-2-100.home.arpa", "host-192-0-2-101.home.arpa", "host-192-0-2-102.home.arpa", "host-192-0-2-103.home.arpa", "host-192-0-2-104.home.arpa"} {
		got := v.Name(rows[i].Address, now)
		assert.Equal(t, want, got.Hostname)
		assert.True(t, got.Generated)
	}
	assert.Equal(t, "host-office.home.arpa", v.Name(ip("192.0.2.105"), now).Hostname)
	assert.False(t, v.Name(ip("192.0.2.105"), now).Generated)
	reserved := v.Name(ip("192.0.2.20"), now)
	assert.Equal(t, "reserved.home.arpa", reserved.Hostname)
	assert.True(t, reserved.Reservation)
	assert.False(t, reserved.Generated)
}

func TestLeaseDirectAnswersAndAllocationBudget(t *testing.T) {
	now := time.Now()
	z, err := localdns.Build(nil, nil)
	require.NoError(t, err)
	v := localdns.BuildLeases(1, "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("192.0.2.100"), Hostname: "desk", Expiry: now.Add(time.Minute)}}, nil, z)
	for _, tc := range []struct {
		name          string
		typ           uint16
		code, answers int
	}{
		{"desk.home.arpa.", 1, 0, 1}, {"100.2.0.192.in-addr.arpa.", 12, 0, 1},
		{"desk.home.arpa.", 28, 0, 0}, {"absent.home.arpa.", 1, 3, 0},
		{"home.arpa.", 6, 0, 1}, {"home.arpa.", 1, 0, 0},
	} {
		t.Run(tc.name+dns.TypeToString[tc.typ], func(t *testing.T) {
			q := new(dns.Msg)
			q.SetQuestion(tc.name, tc.typ)
			b, e := q.Pack()
			require.NoError(t, e)
			var m dnswire.Message
			require.NoError(t, dnswire.ParseRequest(b, &m))
			out := make([]byte, 1232)
			var n int
			var ok bool
			allocs := testing.AllocsPerRun(100, func() { n, ok, e = z.AnswerWithLeases(out, &m, v, now) })
			require.NoError(t, e)
			require.True(t, ok)
			var got dns.Msg
			require.NoError(t, got.Unpack(out[:n]))
			assert.Equal(t, tc.code, got.Rcode)
			assert.Len(t, got.Answer, tc.answers)
			assert.Zero(t, allocs, "lease wire data must be prepared at publication")
		})
	}
}

func TestLeaseApexWithUnrelatedExplicitData(t *testing.T) {
	z, e := localdns.Build(nil, []localdns.Record{{Name: "fixed.test", Type: "A", Value: "192.0.2.1", TTL: 60}})
	require.NoError(t, e)
	v := localdns.BuildLeases(1, "home.arpa", nil, nil, z)
	q := new(dns.Msg)
	q.SetQuestion("home.arpa.", 6)
	b, e := q.Pack()
	require.NoError(t, e)
	var m dnswire.Message
	require.NoError(t, dnswire.ParseRequest(b, &m))
	out := make([]byte, 1232)
	n, ok, e := z.AnswerWithLeases(out, &m, v, time.Now())
	require.NoError(t, e)
	require.True(t, ok)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, 0, got.Rcode)
	assert.Len(t, got.Answer, 1)
}

func TestReservationDisplayNameSurvivesLeaseExpiryWithoutServingDNS(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	v := localdns.BuildLeases(1, "home.arpa", []localdns.Lease{{Address: ip, Hostname: "client-claim", Expiry: now.Add(-time.Second)}}, map[netip.Addr]string{ip: "lab-printer"}, nil)
	n := v.Name(ip, now)
	assert.Equal(t, "lab-printer.home.arpa", n.Hostname)
	assert.True(t, n.Reservation)
	q := new(dns.Msg)
	q.SetQuestion("lab-printer.home.arpa.", 1)
	b, e := q.Pack()
	require.NoError(t, e)
	var m dnswire.Message
	require.NoError(t, dnswire.ParseRequest(b, &m))
	out := make([]byte, 1232)
	size, ok, e := v.Answer(out, &m, now)
	require.NoError(t, e)
	require.True(t, ok)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:size]))
	assert.Equal(t, 3, got.Rcode)
	assert.Empty(t, got.Answer)
}

func TestReservationRenameReleasesOldDynamicLabel(t *testing.T) {
	now := time.Now()
	dynamic := netip.MustParseAddr("192.0.2.100")
	reserved := netip.MustParseAddr("192.0.2.20")
	v := localdns.BuildLeases(2, "home.arpa", []localdns.Lease{{Address: dynamic, Hostname: "desk", Expiry: now.Add(time.Minute)}, {Address: reserved, Hostname: "desk", Expiry: now.Add(time.Minute)}}, map[netip.Addr]string{reserved: "printer"}, nil)
	assert.Equal(t, "desk.home.arpa", v.Name(dynamic, now).Hostname)
	assert.Equal(t, "printer.home.arpa", v.Name(reserved, now).Hostname)
}

func TestLeaseExplicitWildcardAndReversePrecedence(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.100")
	z, err := localdns.Build(nil, []localdns.Record{
		{Name: "home.arpa", Match: "suffix", Type: "AAAA", Value: "2001:db8::1", TTL: 80},
		{Name: "100.2.0.192.in-addr.arpa", Type: "PTR", Value: "configured.test", TTL: 80},
	})
	require.NoError(t, err)
	v := localdns.BuildLeases(1, "home.arpa", []localdns.Lease{{Address: ip, Hostname: "desk", Expiry: now.Add(time.Minute)}}, nil, z)
	for _, tc := range []struct {
		name    string
		typ     uint16
		answers int
	}{
		{"desk.home.arpa.", 1, 0}, {"desk.home.arpa.", 28, 1}, {"100.2.0.192.in-addr.arpa.", 12, 1},
	} {
		q := new(dns.Msg)
		q.SetQuestion(tc.name, tc.typ)
		b, e := q.Pack()
		require.NoError(t, e)
		var m dnswire.Message
		require.NoError(t, dnswire.ParseRequest(b, &m))
		out := make([]byte, 1232)
		n, ok, e := z.AnswerWithLeases(out, &m, v, now)
		require.NoError(t, e)
		require.True(t, ok)
		var got dns.Msg
		require.NoError(t, got.Unpack(out[:n]))
		assert.Equal(t, 0, got.Rcode)
		require.Len(t, got.Answer, tc.answers)
		if tc.answers > 0 {
			assert.EqualValues(t, 80, got.Answer[0].Header().Ttl)
		}
	}
}
