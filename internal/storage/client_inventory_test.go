package storage

import (
	"database/sql"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientInventoryMigrationCoversCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v6.sqlite")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(initialSchema + bootCoverageSchema + ruleSourceSchema + latencySchema + queryResponseSchema + clientNamesSchema)
	require.NoError(t, err)
	_, err = raw.Exec(`INSERT INTO domains VALUES(1,x'016100');
INSERT INTO clients VALUES(1,zeroblob(16));
INSERT INTO query_events(boot_id,sequence,timestamp,duration,domain_id,client_id,qtype,qclass,outcome,rcode,upstream_id,generation,rule_id,flags)
VALUES('migration',1,?,1,1,1,1,1,?,0,0,1,0,0)`, testStart.UnixMicro(), stats.PolicyBlock)
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	for range 2 {
		d, err := Open(path)
		require.NoError(t, err)
		page, err := d.GetClients(t.Context(), testStart, testStart.Add(time.Hour), 200)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.Equal(t, uint64(1), page.Items[0].Count)
		assert.Equal(t, uint64(1), page.Items[0].Blocked)
		// Counts must not fetch the large retained response rows. This probes the
		// covering property, not an exact index definition or planner rendering.
		rows, err := d.read.Query(`EXPLAIN QUERY PLAN SELECT timestamp,outcome FROM query_events INDEXED BY events_client_time WHERE client_id=1 AND timestamp>=0`)
		require.NoError(t, err)
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
			plan = append(plan, detail)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		assert.Contains(t, strings.Join(plan, "\n"), "COVERING INDEX")
		require.NoError(t, d.Close())
	}
}

func TestClientInventoryWindowRetentionAndTies(t *testing.T) {
	d := openTest(t)
	a := netip.MustParseAddr("192.0.2.1").As16()
	b := netip.MustParseAddr("2001:db8::1").As16()
	var events []stats.QueryEvent
	for i, entry := range []struct {
		address [16]byte
		minute  int
		outcome stats.Outcome
	}{
		{a, -1, stats.PolicyBlock}, // outside the requested window
		{a, 0, stats.PolicyBlock},  // logically expired
		{a, 1, stats.PolicyBlock},
		{a, 2, stats.FreshCache},
		{b, 1, stats.FreshCache},
		{b, 3, stats.FreshCache},
		{b, 4, stats.AdmissionRejected},
		{b, 60, stats.PolicyBlock}, // exclusive end
	} {
		e := event(uint64(i + 1))
		e.Client, e.Timestamp, e.Outcome = entry.address, testStart.Add(time.Duration(entry.minute)*time.Minute).UnixMicro(), entry.outcome
		events = append(events, e)
	}
	require.NoError(t, d.WriteBatch(t.Context(), "inventory", events))
	_, err := d.write.Exec("UPDATE storage_meta SET value=? WHERE key='detail_cutoff'", testStart.Add(time.Minute).UnixMicro())
	require.NoError(t, err)
	page, err := d.GetClients(t.Context(), testStart, testStart.Add(time.Hour), 200)
	require.NoError(t, err)
	assert.Equal(t, []ObservedClient{
		{Address: a, Count: 2, Blocked: 1, LastSeen: testStart.Add(2 * time.Minute)},
		{Address: b, Count: 2, Blocked: 0, LastSeen: testStart.Add(3 * time.Minute)},
	}, page.Items)
	assert.False(t, page.Complete)
	assert.False(t, page.Truncated)
	limited, err := d.GetClients(t.Context(), testStart, testStart.Add(time.Hour), 1)
	require.NoError(t, err)
	assert.Equal(t, page.Items[:1], limited.Items)
	assert.True(t, limited.Truncated)
	empty, err := d.GetClients(t.Context(), testStart.Add(5*time.Minute), testStart.Add(time.Hour), 200)
	require.NoError(t, err)
	assert.Empty(t, empty.Items)
}

// BenchmarkClientInventory uses 300k retained events spread over seven days and
// 40 documentation addresses. Large response payloads exercise index-only reads.
func BenchmarkClientInventory(b *testing.B) {
	d, err := Open(filepath.Join(b.TempDir(), "history.sqlite"))
	require.NoError(b, err)
	b.Cleanup(func() { assert.NoError(b, d.Close()) })
	_, err = d.write.Exec(`INSERT INTO domains VALUES(1,x'016100');
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<40)
INSERT INTO clients SELECT x,CAST(x'00000000000000000000ffffc00002' || char(x) AS BLOB) FROM n;
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<300000)
INSERT INTO query_events(boot_id,sequence,timestamp,duration,domain_id,client_id,qtype,qclass,outcome,rcode,upstream_id,generation,rule_id,flags,response)
SELECT 'synthetic',x,?+x*2016000,100,1,1+x%40,1,1,x%6,0,0,1,0,0,zeroblob(600) FROM n`, testStart.UnixMicro())
	require.NoError(b, err)
	for _, window := range []struct {
		name     string
		duration time.Duration
	}{
		{"hour", time.Hour}, {"week", 7 * 24 * time.Hour},
	} {
		b.Run(window.name, func(b *testing.B) {
			var page ClientPage
			var err error
			for b.Loop() {
				page, err = d.GetClients(b.Context(), testStart, testStart.Add(window.duration), 200)
				if err != nil {
					b.Fatal(err)
				}
			}
			require.Len(b, page.Items, 40)
		})
	}
	var sequence uint64
	b.Run("write100", func(b *testing.B) {
		events := make([]stats.QueryEvent, 100)
		for b.Loop() {
			for i := range events {
				sequence++
				events[i] = event(sequence)
				events[i].Client = netip.AddrFrom4([4]byte{192, 0, 2, byte(1 + i%40)}).As16()
				events[i].Timestamp = testStart.Add(7*24*time.Hour).UnixMicro() + int64(sequence)
			}
			if err := d.WriteBatch(b.Context(), "writer", events); err != nil {
				b.Fatal(err)
			}
		}
	})
}
