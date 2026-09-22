package dhcp

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wall and elapsed time are independent injected sources. No host clock changes
// or synthetic time.Time internals are needed to reproduce a post-restart step.
func TestRecoveredOwnershipSurvivesWallStep(t *testing.T) {
	s, dir, base := storeFixture(t)
	l := storedLease(base, 1)
	l.Address = netip.MustParseAddr("192.0.2.100")
	l.Expiry = base.Add(30 * time.Minute)
	storePut(t, s, 1, l)
	require.NoError(t, s.Close(context.Background()))
	s, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return base.Add(4 * time.Hour) })
	require.NoError(t, err)
	defer s.Close(context.Background())
	wall, elapsed := base, base
	settings := fixtureSettings()
	settings.RangeEnd = settings.RangeStart
	e, err := NewEngine(settings, 2, func() time.Time { return wall })
	require.NoError(t, err)
	e.runtimeClock = func() time.Time { return elapsed }
	require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
	wall = wall.Add(2 * time.Hour)
	elapsed = elapsed.Add(time.Minute)
	require.Empty(t, e.Tick(), "wall step must not expire recovered ownership")
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	assert.Equal(t, []Lease{l}, e.DurableLeases())
	require.NoError(t, e.Apply(settings, 3))
	assert.Equal(t, []Lease{l}, e.Leases())
	// Remaining grant is measured on the elapsed source as well.
	ack := e.ack(e.byIP[l.Address], client(1, RequestMessage), wall)
	require.NotNil(t, ack.Reply)
	assert.Equal(t, 1740, ack.Reply.LeaseSeconds)
	elapsed = base.Add(time.Hour - time.Second)
	require.Empty(t, e.Tick())
	assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
	elapsed = base.Add(time.Hour)
	deletes := e.Tick()
	require.Len(t, deletes, 1)
	assert.Equal(t, l, deletes[0].Lease, "delete must match exact persisted timestamps")
	require.NoError(t, s.Submit(context.Background(), deletes[0]))
	result := storeResult(t, s)
	require.NoError(t, result.Err)
	e.CompleteCommit(result)
	assert.NotNil(t, e.Handle(client(2, Discover)).Probe)
	require.NoError(t, s.Close(context.Background()))
	reopened, recovered, err := OpenLeaseStore(dir, 1024, func() time.Time { return base.Add(4 * time.Hour) })
	require.NoError(t, err)
	defer reopened.Close(context.Background())
	assert.Empty(t, recovered.Leases)
}

func TestRecoveredMutationPreservesElapsedHoldAndDurableHorizon(t *testing.T) {
	for name, kind := range map[string]MessageType{"renewal": RequestMessage, "decline": Decline} {
		t.Run(name, func(t *testing.T) {
			s, dir, base := storeFixture(t)
			l := storedLease(base, 1)
			l.Address = netip.MustParseAddr("192.0.2.100")
			storePut(t, s, 1, l)
			require.NoError(t, s.Close(context.Background()))
			wall, elapsed := base, base
			s, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return base.Add(2 * time.Hour) })
			require.NoError(t, err)
			defer s.Close(context.Background())
			settings := fixtureSettings()
			settings.RangeEnd = settings.RangeStart
			settings.LeaseSeconds = 60
			e, err := NewEngine(settings, 2, func() time.Time { return wall })
			require.NoError(t, err)
			e.runtimeClock = func() time.Time { return elapsed }
			require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
			wall = base.Add(2 * time.Hour)
			elapsed = base.Add(time.Minute)
			r := client(1, kind)
			if kind == Decline {
				r.ServerID = netip.MustParseAddr(settings.ServerIP)
				r.RequestedIP = l.Address
			} else {
				r.CIAddr = l.Address
			}
			out := e.Handle(r)
			require.NotNil(t, out.Mutation)
			assert.Equal(t, base.Add(179*time.Minute), out.Mutation.Lease.HoldUntil)
			assert.Equal(t, []Lease{l}, e.DurableLeases(), "pending mutations retain committed view")
			e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("not admitted")})
			assert.Equal(t, []Lease{l}, e.DurableLeases())
			if kind == Decline {
				wall = wall.Add(time.Minute)
				ms := e.Tick()
				require.Len(t, ms, 1)
				assert.Equal(t, base.Add(180*time.Minute), ms[0].Lease.HoldUntil,
					"retry must persist remaining runtime hold relative to the new wall time")
				out = Outcome{Mutation: &ms[0]}
			} else {
				require.Empty(t, e.Tick(), "failed renewal restores runtime ownership")
				out = e.Handle(r)
			}
			require.NotNil(t, out.Mutation)
			require.NoError(t, s.Submit(context.Background(), *out.Mutation))
			result := storeResult(t, s)
			require.NoError(t, result.Err)
			ack := e.CompleteCommit(result)
			if kind == RequestMessage {
				require.NotNil(t, ack.Reply)
				assert.Equal(t, 60, ack.Reply.LeaseSeconds)
				// Another wall step cannot suppress or lengthen duplicate ACKs.
				wall = wall.Add(time.Hour)
				elapsed = elapsed.Add(10 * time.Second)
				replay := e.Handle(r)
				assert.Nil(t, replay.Mutation)
				require.NotNil(t, replay.Reply)
				assert.Equal(t, 50, replay.Reply.LeaseSeconds)
			}
			require.NoError(t, e.Apply(settings, 3))
			assert.Equal(t, []Lease{out.Mutation.Lease}, e.DurableLeases())
			elapsed = base.Add(time.Hour - time.Second)
			require.Empty(t, e.Tick())
			assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
			// A second recovery with a trustworthy persisted wall clock retains
			// the projected hold, even though the new grant is shorter.
			require.NoError(t, s.Close(context.Background()))
			reopened, recovered, err := OpenLeaseStore(dir, 1024, func() time.Time { return base.Add(2 * time.Hour) })
			require.NoError(t, err)
			defer reopened.Close(context.Background())
			require.Equal(t, []Lease{out.Mutation.Lease}, recovered.Leases)
			fresh, err := NewEngine(settings, 4, func() time.Time { return base.Add(2 * time.Hour) })
			require.NoError(t, err)
			require.NoError(t, fresh.Restore(recovered.Leases, recovered.LastKnown))
			assert.Empty(t, fresh.Tick())
			assert.Equal(t, Outcome{}, fresh.Handle(client(2, Discover)))
		})
	}
}
