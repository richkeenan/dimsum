package dhcp

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"testing"
	"time"
)

func engineFixture(t *testing.T, s Settings) (*Engine, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e, err := NewEngine(s, 1, func() time.Time { return now })
	require.NoError(t, err)
	return e, &now
}
func client(n byte, kind MessageType) Request {
	return Request{Type: kind, MAC: [6]byte{2, 0, 0, 0, 0, n}, XID: uint32(n)}
}
func offered(t *testing.T, e *Engine, r Request) Reply {
	t.Helper()
	out := e.Handle(r)
	require.NotNil(t, out.Probe)
	out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token})
	require.NotNil(t, out.Reply)
	require.Equal(t, Offer, out.Reply.Type)
	return *out.Reply
}
func bind(t *testing.T, e *Engine, r Request) Lease {
	t.Helper()
	o := offered(t, e, r)
	r.Type = RequestMessage
	r.RequestedIP = o.Address
	r.ServerID = netip.MustParseAddr("192.0.2.2")
	out := e.Handle(r)
	require.Nil(t, out.Reply)
	require.NotNil(t, out.Mutation)
	m := *out.Mutation
	out = e.CompleteCommit(CommitResult{Token: m.Token})
	require.NotNil(t, out.Reply)
	assert.Equal(t, ACK, out.Reply.Type)
	return m.Lease
}

func TestEngineDurabilityAndDuplicates(t *testing.T) {
	e, _ := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	o := offered(t, e, r)
	assert.Equal(t, "192.0.2.100", o.Address.String())
	r.Type = RequestMessage
	r.RequestedIP = o.Address
	r.ServerID = netip.MustParseAddr("192.0.2.2")
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Nil(t, out.Reply)
	m := *out.Mutation
	assert.Equal(t, Outcome{}, e.Handle(r))
	assert.Equal(t, Outcome{}, e.CompleteCommit(CommitResult{Token: m.Token, Err: errors.New("disk full")}))
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	out = e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	require.NotNil(t, out.Reply)
	assert.Equal(t, ACK, out.Reply.Type)
	out = e.Handle(r)
	assert.Nil(t, out.Mutation)
	require.NotNil(t, out.Reply)
	assert.Equal(t, ACK, out.Reply.Type)
	assert.Equal(t, Outcome{}, e.CompleteCommit(CommitResult{Token: m.Token}))
}

func TestEngineRequestStates(t *testing.T) {
	for _, mode := range []string{"renew", "rebind", "reboot"} {
		t.Run(mode, func(t *testing.T) {
			e, now := engineFixture(t, fixtureSettings())
			r := client(1, Discover)
			l := bind(t, e, r)
			*now = now.Add(time.Minute)
			r.Type = RequestMessage
			r.XID++
			if mode == "reboot" {
				r.RequestedIP = l.Address
			} else {
				r.CIAddr = l.Address
			}
			out := e.Handle(r)
			require.NotNil(t, out.Mutation)
			assert.Nil(t, out.Probe)
			assert.Nil(t, out.Reply)
			assert.True(t, out.Mutation.Lease.Expiry.After(l.Expiry))
			out = e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
			require.NotNil(t, out.Reply)
			assert.Equal(t, ACK, out.Reply.Type)
		})
	}
	e, _ := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	o := offered(t, e, r)
	r.Type = RequestMessage
	r.RequestedIP = o.Address
	r.ServerID = netip.MustParseAddr("192.0.2.3")
	assert.Equal(t, Outcome{}, e.Handle(r))
	r.ServerID = netip.Addr{}
	r.RequestedIP = netip.MustParseAddr("198.51.100.3")
	out := e.Handle(r)
	require.NotNil(t, out.Reply)
	assert.Equal(t, NAK, out.Reply.Type)
	r = client(2, Inform)
	r.CIAddr = netip.MustParseAddr("192.0.2.50")
	out = e.Handle(r)
	require.NotNil(t, out.Reply)
	assert.Equal(t, ACK, out.Reply.Type)
	assert.False(t, out.Reply.Address.IsValid())
	assert.Nil(t, out.Mutation)
	r.RelayIP = netip.MustParseAddr("192.0.2.1")
	assert.Equal(t, Outcome{}, e.Handle(r))
}

func TestEngineReleaseDeclineExpiryAndCapacity(t *testing.T) {
	s := fixtureSettings()
	s.RangeEnd = s.RangeStart
	s.MaxLeases = 1
	e, now := engineFixture(t, s)
	r := client(1, Discover)
	l := bind(t, e, r)
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	r.Type = Release
	r.CIAddr = l.Address
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Nil(t, out.Reply)
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("disk")})
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Nil(t, e.CompleteCommit(CommitResult{Token: out.Mutation.Token}).Reply)
	o := offered(t, e, client(2, Discover))
	*now = now.Add(31 * time.Second)
	e.Tick()
	require.NotNil(t, e.Handle(client(3, Discover)).Probe)
	e, now = engineFixture(t, s)
	r = client(1, Decline)
	r.RequestedIP = o.Address
	r.ServerID = netip.MustParseAddr(s.ServerIP)
	assert.Equal(t, Outcome{}, e.Handle(r))
	o = offered(t, e, client(1, Discover))
	out = e.Handle(r)
	assert.Nil(t, out.Reply)
	require.NotNil(t, out.Mutation)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	assert.Equal(t, Quarantined, e.Leases()[0].State)
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	*now = now.Add(10*time.Minute + time.Second)
	for _, m := range e.Tick() {
		e.CompleteCommit(CommitResult{Token: m.Token})
	}
	require.NotNil(t, e.Handle(client(2, Discover)).Probe)
}

func TestEngineProbeBoundsAndGeneration(t *testing.T) {
	e, now := engineFixture(t, fixtureSettings())
	a := e.Handle(client(1, Discover))
	require.NotNil(t, a.Probe)
	assert.Equal(t, Outcome{}, e.Handle(client(1, Discover)))
	b := e.Handle(client(2, Discover))
	require.NotNil(t, b.Probe)
	assert.Equal(t, Outcome{}, e.Handle(client(3, Discover)))
	out := e.CompleteProbe(ProbeResult{Token: a.Probe.Token, Conflict: true})
	require.NotNil(t, out.Mutation)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	require.NotNil(t, out.Probe)
	out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token, Conflict: true})
	require.NotNil(t, out.Mutation)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	require.NotNil(t, out.Probe)
	out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token, Conflict: true})
	require.NotNil(t, out.Mutation)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	assert.Nil(t, out.Probe)
	assert.Nil(t, out.Reply)
	*now = now.Add(time.Second)
	e.Tick()
	assert.Nil(t, e.CompleteProbe(ProbeResult{Token: b.Probe.Token}).Reply)
	a = e.Handle(client(4, Discover))
	require.NotNil(t, a.Probe)
	s := fixtureSettings()
	s.LeaseSeconds = 120
	require.NoError(t, e.Apply(s, 2))
	assert.Equal(t, Outcome{}, e.CompleteProbe(ProbeResult{Token: a.Probe.Token}))
}

func TestEngineReservationsAndReconfiguration(t *testing.T) {
	s := fixtureSettings()
	s.Reservations = []Reservation{{ID: "mac", MAC: "02:00:00:00:00:01", Address: "192.0.2.20"}, {ID: "id", ClientID: "0102", Address: "192.0.2.21"}}
	e, now := engineFixture(t, s)
	r := client(1, Discover)
	r.ClientID = string([]byte{1, 2})
	l := bind(t, e, r)
	assert.Equal(t, "192.0.2.21", l.Address.String())
	s.Reservations[1].ClientID = "0103"
	assert.Error(t, e.Apply(s, 2))
	s.Reservations = nil
	s.RangeStart = "192.0.2.150"
	require.NoError(t, e.Apply(s, 2))
	assert.Equal(t, l.Address, e.Leases()[0].Address)
	s.Interface = "eth1"
	assert.Error(t, e.Apply(s, 3))
	s.Enabled = false
	require.NoError(t, e.Apply(s, 3))
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	assert.Len(t, e.Leases(), 1)
	*now = now.Add(-time.Hour)
	e.Tick()
	assert.True(t, e.ClockSuspended())
	assert.Len(t, e.Leases(), 1)
}

func TestEngineRecoveryAndDurableExpiry(t *testing.T) {
	s := fixtureSettings()
	e, now := engineFixture(t, s)
	l := Lease{Identity: "mac:" + string([]byte{2, 0, 0, 0, 0, 1}), MAC: [6]byte{2, 0, 0, 0, 0, 1}, Address: netip.MustParseAddr("192.0.2.100"), State: Bound, Expiry: now.Add(time.Minute)}
	l.HoldUntil = l.Expiry
	require.NoError(t, e.Restore([]Lease{l}, *now))
	assert.Error(t, e.Restore([]Lease{l}, *now))
	*now = now.Add(2 * time.Minute)
	ms := e.Tick()
	require.Len(t, ms, 1)
	assert.Equal(t, DeleteLease, ms[0].Kind)
	assert.Error(t, e.Apply(s, 2))
	e.CompleteCommit(CommitResult{Token: ms[0].Token, Err: errors.New("disk")})
	assert.Len(t, e.Leases(), 1)
	ms = e.Tick()
	require.Len(t, ms, 1)
	e.CompleteCommit(CommitResult{Token: ms[0].Token})
	assert.Empty(t, e.Leases())
	e, now = engineFixture(t, s)
	assert.Error(t, e.Restore([]Lease{l, l}, *now))
	assert.Empty(t, e.Leases())
	assert.Error(t, e.Restore([]Lease{l}, now.Add(time.Hour)))
	assert.Empty(t, e.Leases())
	l.Identity = "id:" + string(make([]byte, 256))
	assert.Error(t, e.Restore([]Lease{l}, *now))
}

func TestReservationCapacityAndIdentityIsolation(t *testing.T) {
	s := fixtureSettings()
	s.MaxLeases = 2
	s.Reservations = []Reservation{{ID: "fixed", MAC: "02:00:00:00:00:02", Address: "192.0.2.20"}}
	e, _ := engineFixture(t, s)
	bind(t, e, client(1, Discover))
	assert.Equal(t, Outcome{}, e.Handle(client(3, Discover)))
	l := bind(t, e, client(2, Discover))
	assert.Equal(t, "192.0.2.20", l.Address.String())
	s = fixtureSettings()
	e, _ = engineFixture(t, s)
	r := client(1, Discover)
	r.ClientID = "one"
	a := bind(t, e, r)
	r.ClientID = "two"
	b := bind(t, e, r)
	assert.NotEqual(t, a.Address, b.Address)
}

func TestRediscoverUpdatesTransactionAndRenewUpdatesMAC(t *testing.T) {
	e, _ := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	r.ClientID = "stable"
	o := offered(t, e, r)
	r.XID++
	require.NotNil(t, e.Handle(r).Reply)
	r.Type = RequestMessage
	r.RequestedIP = o.Address
	r.ServerID = netip.MustParseAddr("192.0.2.2")
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	r.XID++
	r.MAC[5] = 9
	r.CIAddr = o.Address
	r.RequestedIP = netip.Addr{}
	r.ServerID = netip.Addr{}
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Equal(t, r.MAC, out.Mutation.Lease.MAC)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	assert.Equal(t, r.MAC, e.Leases()[0].MAC)
}

func TestEngineCapacityPlateaus(t *testing.T) {
	for _, capacity := range []int{1024, 4096} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			s := fixtureSettings()
			s.MaxLeases = capacity
			s.Subnet = "192.0.0.0/16"
			s.RangeStart = "192.0.16.0"
			s.RangeEnd = "192.0.63.255"
			e, _ := engineFixture(t, s)
			for i := 0; i < capacity+100; i++ {
				r := client(1, Discover)
				r.ClientID = fmt.Sprintf("client-%d", i)
				out := e.Handle(r)
				if i < capacity {
					require.NotNil(t, out.Probe)
					out = e.CompleteProbe(ProbeResult{Token: out.Probe.Token})
					require.NotNil(t, out.Reply)
				} else {
					assert.Equal(t, Outcome{}, out)
				}
			}
			assert.Len(t, e.Leases(), capacity)
		})
	}
}

func TestACKNeverExtendsPastDurableExpiry(t *testing.T) {
	e, now := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	l := bind(t, e, r)
	*now = now.Add(time.Minute)
	r.Type = RequestMessage
	r.RequestedIP = l.Address
	r.ServerID = netip.MustParseAddr("192.0.2.2")
	out := e.Handle(r)
	require.NotNil(t, out.Reply)
	assert.Equal(t, 86340, out.Reply.LeaseSeconds)
	assert.Nil(t, out.Mutation)
	r.XID++
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	*now = now.Add(2 * time.Second)
	out = e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	require.NotNil(t, out.Reply)
	assert.Equal(t, 86398, out.Reply.LeaseSeconds)
	r.Type = Inform
	r.CIAddr = l.Address
	r.RequestedIP = netip.Addr{}
	r.ServerID = netip.Addr{}
	out = e.Handle(r)
	require.NotNil(t, out.Reply)
	assert.Zero(t, out.Reply.LeaseSeconds)
}

func TestPendingProbeCoalescesLatestTransaction(t *testing.T) {
	e, _ := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	out := e.Handle(r)
	require.NotNil(t, out.Probe)
	token := out.Probe.Token
	r.XID++
	assert.Equal(t, Outcome{}, e.Handle(r))
	out = e.CompleteProbe(ProbeResult{Token: token})
	require.NotNil(t, out.Reply)
	assert.Equal(t, r.XID, out.Reply.Request.XID)
}

func TestRecoveredOffSubnetLeaseRejectsActivation(t *testing.T) {
	e, now := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	l := Lease{Identity: identity(r), MAC: r.MAC, Address: netip.MustParseAddr("198.51.100.5"), State: Bound, Expiry: now.Add(time.Hour)}
	l.HoldUntil = l.Expiry
	require.Error(t, e.Restore([]Lease{l}, *now))
	assert.Empty(t, e.Leases())
}

func TestDurableViewSurvivesPendingAndFailedRenewal(t *testing.T) {
	e, now := engineFixture(t, fixtureSettings())
	r := client(1, Discover)
	l := bind(t, e, r)
	*now = now.Add(time.Minute)
	r.Type = RequestMessage
	r.CIAddr = l.Address
	r.XID++
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Equal(t, []Lease{l}, e.DurableLeases())
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("disk full")})
	assert.Equal(t, []Lease{l}, e.DurableLeases())
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token})
	assert.Equal(t, []Lease{out.Mutation.Lease}, e.DurableLeases())
}
