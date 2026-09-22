package storage

import (
	"context"
	"fmt"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"testing"
	"time"
)

func TestDomainActivityFiltersAndRetainedTimestamps(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	now := testStart.Add(2 * time.Hour)
	var events []stats.QueryEvent
	for i, domain := range []string{"fw-eventstream.ring.com", "fw-eventstream.ring.com", "example-ats.iot.eu-west-1.amazonaws.com", "ring.com", "fw-eventstream.ring.com.example.org", "fw-eventstream.ring.com"} {
		e := event(uint64(i + 1))
		e.Client = netip.MustParseAddr("192.0.2.20").As16()
		e.Timestamp = now.Add(time.Duration(i) * time.Minute).UnixMicro()
		n, err := policy.NormalizeName(domain)
		require.NoError(t, err)
		e.QNameLength = uint8(n.CopyWire(e.QName[:]))
		if i == 5 {
			e.Outcome = stats.AdmissionRejected
		}
		events = append(events, e)
	}
	require.NoError(t, d.WriteBatch(ctx, "boot", events))
	rows, truncated, err := d.DomainActivity(ctx, now, now.Add(time.Hour), []string{"fw-eventstream.ring.com"}, []string{"amazonaws.com"})
	require.NoError(t, err)
	assert.False(t, truncated)
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, netip.MustParseAddr("192.0.2.20"), r.Address)
		if r.Domain == "fw-eventstream.ring.com" {
			assert.Equal(t, uint64(2), r.Count)
			assert.Equal(t, now, r.First)
			assert.Equal(t, now.Add(time.Minute), r.Last)
		}
	}
	// Advancing the logical retention cutoff must hide rows before physical GC.
	_, err = d.write.Exec("UPDATE storage_meta SET value=? WHERE key='detail_cutoff'", now.Add(90*time.Second).UnixMicro())
	require.NoError(t, err)
	rows, _, err = d.DomainActivity(ctx, now, now.Add(time.Hour), []string{"fw-eventstream.ring.com"}, []string{"amazonaws.com"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "example-ats.iot.eu-west-1.amazonaws.com", rows[0].Domain)
	_, _, err = d.DomainActivity(ctx, now, now.Add(time.Hour), nil, nil)
	assert.Error(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = d.DomainActivity(canceled, now, now.Add(time.Hour), []string{"fw-eventstream.ring.com"}, nil)
	assert.Error(t, err)
}

func TestDomainActivityRefusesTruncatedAttribution(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	n, err := policy.NormalizeName("fw-eventstream.ring.com")
	require.NoError(t, err)
	events := make([]stats.QueryEvent, 4097)
	for i := range events {
		e := event(uint64(i + 1))
		e.Client = netip.MustParseAddr(fmt.Sprintf("2001:db8::%x", i+1)).As16()
		e.QNameLength = uint8(n.CopyWire(e.QName[:]))
		events[i] = e
	}
	for start := 0; start < len(events); start += MaxBatch {
		require.NoError(t, d.WriteBatch(ctx, "boot", events[start:min(start+MaxBatch, len(events))]))
	}
	rows, truncated, err := d.DomainActivity(ctx, testStart, testStart.Add(time.Hour), []string{"fw-eventstream.ring.com"}, nil)
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Empty(t, rows)
}

func TestDomainActivityKeepsRecentCorroboratingPair(t *testing.T) {
	d := openTest(t)
	n, err := policy.NormalizeName("fw-eventstream.ring.com")
	require.NoError(t, err)
	now := testStart.Add(24 * time.Hour)
	var events []stats.QueryEvent
	for i, age := range []time.Duration{24*time.Hour - time.Second, time.Minute, time.Second, 0} {
		e := event(uint64(i + 1))
		e.Timestamp = now.Add(-age).UnixMicro()
		e.QNameLength = uint8(n.CopyWire(e.QName[:]))
		events = append(events, e)
	}
	require.NoError(t, d.WriteBatch(context.Background(), "boot", events))
	rows, truncated, err := d.DomainActivity(context.Background(), testStart, now, []string{"fw-eventstream.ring.com"}, nil)
	require.NoError(t, err)
	assert.False(t, truncated)
	require.Len(t, rows, 1)
	assert.Equal(t, now.Add(-time.Minute), rows[0].Corroborated)
	assert.Equal(t, testStart.Add(time.Second), rows[0].First)
}
