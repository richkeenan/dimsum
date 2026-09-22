package dhcp

import (
	"errors"
	"net/netip"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fuzzGrant struct {
	identity string
	until    time.Time
}
type fuzzCoverage struct{ acks, puts, deletes, quarantines, failures, restores int }

// The independent store and received-ACK ledger catch lost holds, not just
// duplicate map keys. Pending tokens survive arbitrary delays/failures/replays.
func exerciseEngine(t *testing.T, data []byte) fuzzCoverage {
	t.Helper()
	s := fixtureSettings()
	s.MaxLeases = 8
	s.LeaseSeconds = 600
	e, now := engineFixture(t, s)
	generation := uint64(1)
	xid := uint32(100)
	store := map[netip.Addr]Lease{}
	grants := map[netip.Addr]fuzzGrant{}
	offers := map[byte]Reply{}
	var probes []Probe
	var mutations []Mutation
	var lastCommit CommitResult
	var coverage fuzzCoverage
	key := func(r Request) string {
		if r.ClientID != "" {
			return "id:" + r.ClientID
		}
		return "mac:" + string(r.MAC[:])
	}
	var record func(Outcome)
	record = func(out Outcome) {
		if out.Probe != nil {
			probes = append(probes, *out.Probe)
		}
		if out.Mutation != nil {
			m := *out.Mutation
			mutations = append(mutations, m)
			if m.Kind == PutLease {
				if previous, ok := store[m.Lease.Address]; ok {
					require.False(t, m.Lease.HoldUntil.Before(previous.HoldUntil), "put shortened durable hold")
				}
			}
		}
		if out.Reply == nil {
			return
		}
		r := out.Reply
		if r.Type == Offer {
			offers[r.Request.MAC[5]] = *r
		}
		if r.Type == ACK && r.Address.IsValid() {
			coverage.acks++
			durable, ok := store[r.Address]
			require.True(t, ok, "ACK without durable row")
			require.Equal(t, key(r.Request), durable.Identity)
			until := now.Add(time.Duration(r.LeaseSeconds) * time.Second)
			require.False(t, durable.Expiry.Before(until))
			require.False(t, durable.HoldUntil.Before(until))
			if old, ok := grants[r.Address]; ok && now.Before(old.until) {
				require.Equal(t, old.identity, key(r.Request), "second acknowledged owner before prior promise ended")
				if old.until.After(until) {
					until = old.until
				}
			}
			grants[r.Address] = fuzzGrant{key(r.Request), until}
		}
	}
	completeProbe := func(conflict bool) {
		if len(probes) == 0 {
			return
		}
		p := probes[0]
		probes = probes[1:]
		record(e.CompleteProbe(ProbeResult{Token: p.Token, Conflict: conflict}))
	}
	completeCommit := func(fail bool) {
		if len(mutations) == 0 {
			return
		}
		m := mutations[0]
		mutations = mutations[1:]
		result := CommitResult{Token: m.Token}
		if fail {
			result.Err = errors.New("injected definite rollback")
			coverage.failures++
		} else if m.Kind == DeleteLease {
			delete(store, m.Lease.Address)
			coverage.deletes++
		} else {
			store[m.Lease.Address] = m.Lease
			coverage.puts++
			if m.Lease.State == Quarantined {
				coverage.quarantines++
			}
		}
		lastCommit = result
		record(e.CompleteCommit(result))
	}
	requestOffer := func(n byte) {
		if o, ok := offers[n]; ok {
			r := o.Request
			r.Type = RequestMessage
			r.RequestedIP = o.Address
			r.ServerID = netip.MustParseAddr(s.ServerIP)
			r.CIAddr = netip.Addr{}
			record(e.Handle(r))
		}
	}
	bound := func(n byte) (Lease, bool) {
		for _, l := range e.DurableLeases() {
			if l.MAC[5] == n && l.State == Bound {
				return l, true
			}
		}
		return Lease{}, false
	}
	renew := func(n byte, reboot bool) {
		if l, ok := bound(n); ok {
			r := client(n, RequestMessage)
			xid++
			r.XID = xid
			if reboot {
				r.RequestedIP = l.Address
			} else {
				r.CIAddr = l.Address
			}
			record(e.Handle(r))
		}
	}
	check := func() {
		rows := e.DurableLeases()
		expected := make([]Lease, 0, len(store))
		for _, l := range store {
			expected = append(expected, l)
		}
		sort.Slice(expected, func(i, j int) bool { return expected[i].Address.Less(expected[j].Address) })
		require.Equal(t, expected, rows)
		leases := e.Leases()
		require.LessOrEqual(t, len(leases), s.Capacity())
		held := map[netip.Addr]Lease{}
		for _, l := range leases {
			held[l.Address] = l
		}
		for ip, g := range grants {
			if now.Before(g.until) {
				l, ok := held[ip]
				require.True(t, ok, "forgot previously ACKed ownership")
				require.Equal(t, g.identity, l.Identity)
				require.False(t, l.HoldUntil.Before(g.until), "hold shorter than received ACK")
			}
		}
		// Check both directions of the identity index, including concurrent quarantine
		// and replacement-candidate entries for one identity.
		for id, v := range e.byID {
			require.Same(t, v, e.byIP[v.lease.Address])
			require.Equal(t, id, v.lease.Identity)
			require.False(t, v.dirtyQuarantine)
			require.NotEqual(t, Quarantined, v.lease.State)
		}
		for _, v := range e.byIP {
			if v.lease.State != Quarantined && !v.dirtyQuarantine && !(v.lease.State == CommitPending && v.previous.State == Quarantined) {
				require.Same(t, v, e.byID[v.lease.Identity])
			}
		}
	}
	// Every input reaches a real ACKed state via the public event protocol; the
	// previous fuzz harness could never generate a syntactically valid REQUEST.
	record(e.Handle(client(1, Discover)))
	completeProbe(false)
	requestOffer(1)
	completeCommit(false)
	require.Equal(t, 1, coverage.acks)
	check()
	for _, b := range data {
		n := (b>>4)%4 + 1
		switch b % 16 {
		case 0:
			r := client(n, Discover)
			xid++
			r.XID = xid
			record(e.Handle(r))
		case 1:
			completeProbe(b&128 != 0)
		case 2:
			requestOffer(n)
		case 3:
			completeCommit(b&128 != 0)
		case 4:
			renew(n, false) // RENEWING/REBINDING share ciaddr form.
		case 5:
			renew(n, true)
		case 6:
			if l, ok := bound(n); ok {
				r := client(n, Release)
				r.CIAddr = l.Address
				out := e.Handle(r)
				if out.Mutation != nil {
					delete(grants, l.Address)
				}
				record(out)
			}
		case 7:
			r := client(n, Decline)
			r.ServerID = netip.MustParseAddr(s.ServerIP)
			if l, ok := bound(n); ok {
				r.RequestedIP = l.Address
			} else if o, ok := offers[n]; ok {
				r.RequestedIP = o.Address
			}
			record(e.Handle(r))
		case 8:
			*now = now.Add(time.Duration(int(b>>4)*60+1) * time.Second)
			for _, m := range e.Tick() {
				m := m
				record(Outcome{Mutation: &m})
			}
		case 9:
			next := s
			next.LeaseSeconds = 60
			if b&128 != 0 {
				next.LeaseSeconds = 86400
			}
			if e.Apply(next, generation+1) == nil {
				s = next
				generation++
			}
		case 10:
			next := s
			if b&128 != 0 {
				next.RangeStart = "192.0.2.100"
			} else {
				next.RangeStart = "192.0.2.150"
			}
			if e.Apply(next, generation+1) == nil {
				s = next
				generation++
			}
		case 11:
			if len(mutations) == 0 {
				dirty := false
				for _, v := range e.byIP {
					dirty = dirty || v.dirtyQuarantine
				}
				if !dirty {
					rows := make([]Lease, 0, len(store))
					for _, l := range store {
						rows = append(rows, l)
					}
					fresh, err := NewEngine(s, generation+1, func() time.Time { return *now })
					require.NoError(t, err)
					if fresh.Restore(rows, *now) == nil {
						e = fresh
						generation++
						coverage.restores++
					}
				}
			}
		case 12:
			record(e.CompleteCommit(lastCommit))
		case 13:
			next := s.Clone()
			if b&128 != 0 {
				next.Reservations = nil
			} else {
				address := "192.0.2.20"
				if len(next.Reservations) != 0 {
					address = "192.0.2.21"
				}
				next.Reservations = []Reservation{{ID: "fixed", MAC: "02:00:00:00:00:01", Address: address}}
			}
			if e.Apply(next, generation+1) == nil {
				s = next
				generation++
			}
		case 14:
			for len(probes) > 0 {
				completeProbe(false)
			}
			for len(mutations) > 0 {
				completeCommit(false)
			}
		case 15:
			r := client(n, Discover)
			r.ClientID = string([]byte{b, n})
			record(e.Handle(r))
		}
		check()
	}
	return coverage
}

func TestStatefulHarnessExercisesDurableTransitions(t *testing.T) {
	c := exerciseEngine(t, []byte{4, 131, 4, 3, 5, 3, 11, 7, 3, 8, 14, 6, 3, 0, 1, 2, 3})
	assert.GreaterOrEqual(t, c.acks, 3)
	assert.Positive(t, c.failures)
	assert.Positive(t, c.quarantines)
	assert.Positive(t, c.restores)
	c = exerciseEngine(t, []byte{6, 3})
	assert.Positive(t, c.deletes)
	// Shorten, renew/commit, pass the new expiry, recover: old promise must survive.
	exerciseEngine(t, []byte{9, 4, 3, 24, 11})
	// Retire an address then attempt DISCOVER and both request forms.
	exerciseEngine(t, []byte{10, 0, 4, 5, 11, 0, 4})
}

func FuzzEngineOwnership(f *testing.F) {
	f.Add([]byte{0xc7, 0x43, 0xd8}) // Durable quarantine expiry/delete has no identity index.
	f.Add([]byte{4, 131, 4, 3, 5, 3, 11, 7, 3, 8, 14, 6, 3, 0, 1, 2, 3})
	f.Add([]byte{9, 4, 3, 24, 11, 10, 0, 4, 5})
	f.Add([]byte{13, 13, 0, 4, 11, 141, 4, 3}) // Move/remove reservation with retained ownership.
	f.Add([]byte{16, 129, 3, 1, 18, 3, 12})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 512 {
			return
		}
		exerciseEngine(t, data)
	})
}
