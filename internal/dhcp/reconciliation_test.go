package dhcp

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShorterGrantRetainsPromisedOwnershipAcrossLostACKAndRecovery(t *testing.T) {
	s := fixtureSettings()
	s.RangeEnd = s.RangeStart
	e, now := engineFixture(t, s)
	r := client(1, Discover)
	original := bind(t, e, r)
	*now = now.Add(time.Minute)
	s.LeaseSeconds = 60
	require.NoError(t, e.Apply(s, 2))
	r.Type = RequestMessage
	r.XID++
	r.CIAddr = original.Address
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Equal(t, now.Add(time.Minute), out.Mutation.Lease.Expiry)
	assert.Equal(t, original.Expiry, out.Mutation.Lease.HoldUntil)
	// Commit succeeds, ACK is lost. The store row must suffice to recover safety.
	ack := e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	require.NotNil(t, ack.Reply)
	assert.Equal(t, 60, ack.Reply.LeaseSeconds)
	rows := e.DurableLeases()
	recovered, err := NewEngine(s, 3, func() time.Time { return *now })
	require.NoError(t, err)
	require.NoError(t, recovered.Restore(rows, *now))
	*now = now.Add(2 * time.Minute)
	assert.Empty(t, recovered.Tick())
	assert.Equal(t, Outcome{}, recovered.Handle(client(2, Discover)))
	assert.Equal(t, original.Expiry, recovered.DurableLeases()[0].HoldUntil)
	*now = original.Expiry.Add(time.Second)
	ms := recovered.Tick()
	require.Len(t, ms, 1)
	assert.Equal(t, DeleteLease, ms[0].Kind)
	recovered.CompleteCommit(CommitResult{Token: ms[0].Token})
	require.NotNil(t, recovered.Handle(client(2, Discover)).Probe)
}

func TestRetainedTopologyConflictsRejectApplyAndRestore(t *testing.T) {
	for _, kind := range []string{"gateway", "server", "network", "broadcast", "off-subnet"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureSettings()
			s.RangeStart = "192.0.2.127"
			e, now := engineFixture(t, s)
			l := bind(t, e, client(1, Discover))
			s.Enabled = false
			require.NoError(t, e.Apply(s, 2))
			s.Enabled = true
			switch kind {
			case "gateway":
				s.Gateway = l.Address.String()
				s.RangeStart = "192.0.2.150"
			case "server":
				s.ServerIP = l.Address.String()
				s.RangeStart = "192.0.2.150"
			case "network":
				s.Subnet = "192.0.2.128/25"
				s.ServerIP = "192.0.2.130"
				s.Gateway = "192.0.2.129"
				s.RangeStart = "192.0.2.150" // retained address is outside this subnet
			case "broadcast":
				s.Subnet = "192.0.2.0/25"
				s.RangeStart = "192.0.2.100"
				s.RangeEnd = "192.0.2.120"
			case "off-subnet":
				s.Subnet = "198.51.100.0/24"
				s.ServerIP = "198.51.100.2"
				s.Gateway = "198.51.100.1"
				s.RangeStart = "198.51.100.100"
				s.RangeEnd = "198.51.100.199"
			}
			require.NoError(t, ValidateSettings(s))
			err := e.Apply(s, 3)
			require.Error(t, err)
			assert.Contains(t, err.Error(), l.Address.String())
			assert.Contains(t, err.Error(), l.HoldUntil.Format(time.RFC3339))
			assert.Equal(t, []Lease{l}, e.DurableLeases())
			fresh, err := NewEngine(s, 3, func() time.Time { return *now })
			require.NoError(t, err)
			assert.Error(t, fresh.Restore([]Lease{l}, *now))
			assert.Empty(t, fresh.Leases())
		})
	}
	// A formerly usable address can become the exact network address too.
	s := fixtureSettings()
	s.RangeStart = "192.0.2.128"
	e, _ := engineFixture(t, s)
	bind(t, e, client(1, Discover))
	s.Enabled = false
	require.NoError(t, e.Apply(s, 2))
	s.Enabled = true
	s.Subnet = "192.0.2.128/25"
	s.ServerIP = "192.0.2.130"
	s.Gateway = "192.0.2.129"
	s.RangeStart = "192.0.2.150"
	require.NoError(t, ValidateSettings(s))
	assert.Error(t, e.Apply(s, 3))
}

func TestRetiredAddressesCannotBeOfferedOrExtended(t *testing.T) {
	for _, kind := range []string{"pool shrink", "reservation moved", "reservation removed"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureSettings()
			if kind != "pool shrink" {
				s.Reservations = []Reservation{{ID: "fixed", MAC: "02:00:00:00:00:01", Address: "192.0.2.20"}}
			}
			e, now := engineFixture(t, s)
			r := client(1, Discover)
			l := bind(t, e, r)
			switch kind {
			case "pool shrink":
				s.RangeStart = "192.0.2.150"
			case "reservation moved":
				s.Reservations[0].Address = "192.0.2.21"
			case "reservation removed":
				s.Reservations = nil
			}
			require.NoError(t, e.Apply(s, 2))
			assert.Equal(t, Outcome{}, e.Handle(r))
			r.Type = RequestMessage
			r.XID++
			r.CIAddr = l.Address
			assert.Equal(t, Outcome{}, e.Handle(r))
			assert.Equal(t, []Lease{l}, e.DurableLeases())
			*now = l.HoldUntil.Add(time.Second)
			ms := e.Tick()
			require.Len(t, ms, 1)
			e.CompleteCommit(CommitResult{Token: ms[0].Token})
			r.Type = Discover
			r.CIAddr = netip.Addr{}
			out := e.Handle(r)
			require.NotNil(t, out.Probe)
			assert.NotEqual(t, l.Address, out.Probe.Address)
			if kind == "reservation moved" {
				assert.Equal(t, "192.0.2.21", out.Probe.Address.String())
			}
		})
	}
}

func TestNeverBoundQuarantinesAreDurableAndRetried(t *testing.T) {
	for _, kind := range []string{"probe conflict", "offered decline"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureSettings()
			e, now := engineFixture(t, s)
			r := client(1, Discover)
			out := e.Handle(r)
			require.NotNil(t, out.Probe)
			ip := out.Probe.Address
			if kind == "probe conflict" {
				out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token, Conflict: true})
				require.NotNil(t, out.Probe)
			} else {
				out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token})
				r.Type = Decline
				r.ServerID = netip.MustParseAddr(s.ServerIP)
				r.RequestedIP = ip
				out = e.Handle(r)
			}
			require.NotNil(t, out.Mutation)
			q := out.Mutation.Lease
			assert.Equal(t, Quarantined, q.State)
			assert.Equal(t, now.Add(10*time.Minute), q.HoldUntil)
			assert.Empty(t, e.DurableLeases())
			next := out.Probe
			e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("disk full")})
			assert.Equal(t, Quarantined, e.byIP[ip].lease.State)
			*now = now.Add(11 * time.Minute)
			ms := e.Tick()
			require.Len(t, ms, 1)
			assert.Equal(t, PutLease, ms[0].Kind)
			assert.Equal(t, q, ms[0].Lease)
			assert.Error(t, e.Apply(s, 2))
			e.CompleteCommit(CommitResult{Token: ms[0].Token})
			assert.Equal(t, []Lease{q}, e.DurableLeases())
			if next != nil {
				assert.Nil(t, e.CompleteProbe(ProbeResult{Token: next.Token}).Reply)
			}
			recovered, err := NewEngine(s, 2, func() time.Time { return q.Expiry.Add(-time.Minute) })
			require.NoError(t, err)
			require.NoError(t, recovered.Restore([]Lease{q}, q.Expiry.Add(-2*time.Minute)))
			assert.Equal(t, []Lease{q}, recovered.DurableLeases())
			p := recovered.Handle(client(2, Discover))
			require.NotNil(t, p.Probe)
			assert.NotEqual(t, ip, p.Probe.Address)
		})
	}
}

func TestQuarantineCompletionDoesNotRemoveNextCandidateIdentity(t *testing.T) {
	e, _ := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	out := e.Handle(r)
	require.NotNil(t, out.Probe)
	out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token, Conflict: true})
	require.NotNil(t, out.Mutation)
	require.NotNil(t, out.Probe)
	m, p := *out.Mutation, *out.Probe
	out = e.CompleteProbe(ProbeResult{Token: p.Token})
	require.NotNil(t, out.Reply)
	r.Type = RequestMessage
	r.RequestedIP = p.Address
	r.ServerID = netip.MustParseAddr("192.0.2.2")
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	binding := *out.Mutation
	out = e.CompleteCommit(CommitResult{Token: binding.Token})
	require.NotNil(t, out.Reply)
	assert.Equal(t, ACK, out.Reply.Type)
	// Never-bound quarantine persistence need not delay a replacement's ACK.
	require.Len(t, e.DurableLeases(), 1)
	e.CompleteCommit(CommitResult{Token: m.Token})
	out = e.Handle(r)
	require.NotNil(t, out.Reply)
	assert.Equal(t, ACK, out.Reply.Type)
	assert.Len(t, e.DurableLeases(), 2)
}

func TestBoundDeclineKeepsPriorPromiseAndCommittedViewOnFailure(t *testing.T) {
	e, now := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	prior := bind(t, e, r)
	r.Type = Decline
	r.ServerID = netip.MustParseAddr("192.0.2.2")
	r.RequestedIP = prior.Address
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Equal(t, prior.HoldUntil, out.Mutation.Lease.HoldUntil)
	assert.Equal(t, now.Add(10*time.Minute), out.Mutation.Lease.Expiry)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("rollback")})
	assert.Equal(t, []Lease{prior}, e.DurableLeases())
	ms := e.Tick()
	require.Len(t, ms, 1)
	e.CompleteCommit(CommitResult{Token: ms[0].Token})
	*now = now.Add(11 * time.Minute)
	assert.Empty(t, e.Tick())
	assert.Equal(t, prior.HoldUntil, e.DurableLeases()[0].HoldUntil)
}

func TestRecoveryRequiresExplicitConservativeHorizon(t *testing.T) {
	e, now := engineFixture(t, fixtureSettings())
	l := bind(t, e, client(1, Discover))
	for _, horizon := range []time.Time{{}, l.Expiry.Add(-time.Second)} {
		fresh, err := NewEngine(fixtureSettings(), 2, func() time.Time { return *now })
		require.NoError(t, err)
		l.HoldUntil = horizon
		assert.Error(t, fresh.Restore([]Lease{l}, *now))
		assert.Empty(t, fresh.Leases())
	}
}

func TestBoundQuarantineBlocksReplacementUntilDurableAcrossFailureAndCrash(t *testing.T) {
	s := fixtureSettings()
	e, now := engineFixture(t, s)
	r := client(1, Discover)
	original := bind(t, e, r)
	r.Type = Decline
	r.ServerID = netip.MustParseAddr(s.ServerIP)
	r.RequestedIP = original.Address
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	discover := client(1, Discover)
	discover.XID++
	assert.Equal(t, Outcome{}, e.Handle(discover), "pending quarantine must block replacement")
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("definite rollback")})
	for range 3 {
		assert.Equal(t, Outcome{}, e.Handle(discover), "failed quarantine must retain identity barrier")
	}
	rows := e.DurableLeases()
	require.Equal(t, []Lease{original}, rows)
	// Crash before retry: only the actual committed Bound row can be recovered.
	fresh, err := NewEngine(s, 2, func() time.Time { return *now })
	require.NoError(t, err)
	require.NoError(t, fresh.Restore(rows, *now))
	restored := fresh.Handle(discover)
	require.NotNil(t, restored.Reply)
	assert.Equal(t, original.Address, restored.Reply.Address)
	assert.Nil(t, restored.Probe)
	// On the original owner, failed retries still cannot unblock this identity.
	ms := e.Tick()
	require.Len(t, ms, 1)
	e.CompleteCommit(CommitResult{Token: ms[0].Token, Err: errors.New("queue full")})
	assert.Equal(t, Outcome{}, e.Handle(discover))
	ms = e.Tick()
	require.Len(t, ms, 1)
	assert.Equal(t, Outcome{}, e.Handle(discover))
	e.CompleteCommit(CommitResult{Token: ms[0].Token})
	replacement := bind(t, e, discover)
	assert.NotEqual(t, original.Address, replacement.Address)
	rows = e.DurableLeases()
	require.Len(t, rows, 2)
	assert.Equal(t, Quarantined, rows[0].State)
	assert.Equal(t, Bound, rows[1].State)
	fresh, err = NewEngine(s, 3, func() time.Time { return *now })
	require.NoError(t, err)
	require.NoError(t, fresh.Restore(rows, *now))
	assert.Equal(t, rows, fresh.DurableLeases())
}
