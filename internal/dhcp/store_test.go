package dhcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

func storeFixture(t *testing.T) (*LeaseStore, string, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 22, 12, 0, 0, 123, time.UTC)
	dir := filepath.Join(t.TempDir(), "dhcp")
	s, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	require.Empty(t, recovery.Leases)
	t.Cleanup(func() { require.NoError(t, s.Close(context.Background())) })
	return s, dir, now
}

func storedLease(now time.Time, n int) Lease {
	mac := [6]byte{2, 0, 0, byte(n >> 16), byte(n >> 8), byte(n)}
	// IANA's benchmarking range accommodates all 4096 synthetic rows without
	// spilling out of a documentation /24 into globally assigned addresses.
	return Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: netip.AddrFrom4([4]byte{198, 18, byte(n >> 8), byte(n)}), Hostname: "fixture", Expiry: now.Add(time.Minute), HoldUntil: now.Add(time.Hour), State: Bound}
}

func storePut(t *testing.T, s *LeaseStore, sequence uint64, l Lease) {
	t.Helper()
	require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, sequence}, Kind: PutLease, Lease: l}))
	result := storeResult(t, s)
	require.Equal(t, Token{1, sequence}, result.Token)
	require.NoError(t, result.Err)
}

func TestStoreRenewalProcessCrash(t *testing.T) {
	base := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	now := base.Add(time.Minute)
	if point := os.Getenv("DIMSUM_RENEW_CRASH"); point != "" {
		s, recovery, err := OpenLeaseStore(os.Getenv("DIMSUM_STORE_DIR"), 1024, func() time.Time { return now })
		require.NoError(t, err)
		e, err := NewEngine(fixtureSettings(), 2, func() time.Time { return now })
		require.NoError(t, err)
		require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
		request := client(1, RequestMessage)
		request.XID = 42
		request.CIAddr = netip.MustParseAddr("192.0.2.100")
		out := e.Handle(request)
		require.NotNil(t, out.Mutation)
		require.Nil(t, out.Reply)
		begin := s.begin
		s.begin = func() (leaseTransaction, error) { tx, err := begin(); return crashTransaction{tx, point}, err }
		require.NoError(t, s.Submit(context.Background(), *out.Mutation))
		result := storeResult(t, s)
		require.Equal(t, out.Mutation.Token, result.Token)
		require.NoError(t, result.Err)
		ack := e.CompleteCommit(result)
		require.NotNil(t, ack.Reply)
		require.Equal(t, ACK, ack.Reply.Type)
		_, err = BuildReply(fixtureSettings(), *ack.Reply, 1500)
		require.NoError(t, err)
		os.Exit(23)
	}
	for _, point := range []string{"before", "after", "acked"} {
		t.Run(point, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "dhcp")
			s, _, err := OpenLeaseStore(dir, 1024, func() time.Time { return base })
			require.NoError(t, err)
			e, err := NewEngine(fixtureSettings(), 1, func() time.Time { return base })
			require.NoError(t, err)
			old := bind(t, e, client(1, Discover))
			storePut(t, s, 1, old)
			require.NoError(t, s.Close(context.Background()))
			cmd := exec.Command(os.Args[0], "-test.run=^TestStoreRenewalProcessCrash$")
			cmd.Env = append(os.Environ(), "DIMSUM_RENEW_CRASH="+point, "DIMSUM_STORE_DIR="+dir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.Equal(t, 23, exit.ExitCode(), string(output))
			reopened, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
			require.NoError(t, err)
			defer reopened.Close(context.Background())
			require.Len(t, recovery.Leases, 1)
			got := recovery.Leases[0]
			assert.Equal(t, old.Identity, got.Identity)
			assert.Equal(t, old.Address, got.Address)
			if point == "before" {
				assert.Equal(t, old, got)
			} else {
				assert.Equal(t, old.Expiry.Add(time.Minute), got.Expiry)
				assert.Equal(t, old.HoldUntil.Add(time.Minute), got.HoldUntil)
			}
		})
	}
}

func TestStoreFailedRenewalRetainsOldGrantWithoutACK(t *testing.T) {
	s, dir, now := storeFixture(t)
	e, err := NewEngine(fixtureSettings(), 1, func() time.Time { return now })
	require.NoError(t, err)
	old := bind(t, e, client(1, Discover))
	storePut(t, s, 1, old)
	now = now.Add(time.Minute)
	request := client(1, RequestMessage)
	request.XID = 42
	request.CIAddr = old.Address
	out := e.Handle(request)
	require.NotNil(t, out.Mutation)
	require.Nil(t, out.Reply)
	s.begin = func() (leaseTransaction, error) { return nil, errors.New("definite begin failure") }
	require.NoError(t, s.Submit(context.Background(), *out.Mutation))
	result := storeResult(t, s)
	require.Equal(t, out.Mutation.Token, result.Token)
	require.Error(t, result.Err)
	assert.Nil(t, e.CompleteCommit(result).Reply)
	assert.Equal(t, []Lease{old}, e.DurableLeases())
	require.NoError(t, s.Close(context.Background()))
	reopened, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	defer reopened.Close(context.Background())
	assert.Equal(t, []Lease{old}, recovery.Leases)
}

func TestStoreRejectsCorruptSchemaAndRows(t *testing.T) {
	for _, damage := range []string{"index", "metadata-table", "identity", "identity-type", "horizon", "hostname", "oversized", "capacity"} {
		t.Run(damage, func(t *testing.T) {
			s, dir, now := storeFixture(t)
			storePut(t, s, 1, storedLease(now, 1))
			storePut(t, s, 2, storedLease(now, 2))
			statements := map[string]string{
				"index":          "DROP INDEX bound_identity",
				"metadata-table": "ALTER TABLE metadata RENAME TO wrong_metadata",
				"identity":       "UPDATE leases SET identity=x'62616421' WHERE address='198.18.0.1'",
				"identity-type":  "UPDATE leases SET identity=CAST(identity AS TEXT)",
				"horizon":        "PRAGMA ignore_check_constraints=ON; UPDATE leases SET hold_until=1",
				"hostname":       "UPDATE leases SET hostname='invalid.label'",
			}
			if statement := statements[damage]; statement != "" {
				_, err := s.db.Exec(statement)
				require.NoError(t, err)
			}
			require.NoError(t, s.Close(context.Background()))
			if damage == "oversized" {
				require.NoError(t, os.Truncate(filepath.Join(dir, "leases.sqlite"), storeDBBytes+4096))
			}
			capacity := 1024
			if damage == "capacity" {
				capacity = 1
			}
			other, _, err := OpenLeaseStore(dir, capacity, func() time.Time { return now })
			if other != nil {
				other.Close(context.Background())
			}
			require.Error(t, err)
		})
	}
}

func TestStoreQuarantineAndHoldRoundTrip(t *testing.T) {
	s, dir, now := storeFixture(t)
	l := storedLease(now, 1)
	storePut(t, s, 1, l)
	l.State = Quarantined
	storePut(t, s, 2, l)
	next := l
	next.Address = storedLease(now, 2).Address
	next.State = Bound
	storePut(t, s, 3, next)
	require.NoError(t, s.Close(context.Background()))
	// Expiry has passed but the earlier promise must survive through HoldUntil.
	reopened, r, err := OpenLeaseStore(dir, 1024, func() time.Time { return now.Add(2 * time.Minute) })
	require.NoError(t, err)
	defer reopened.Close(context.Background())
	require.Len(t, r.Leases, 2)
	assert.Equal(t, l, r.Leases[0])
	assert.Equal(t, next, r.Leases[1])
	e, err := NewEngine(Settings{MaxLeases: 1024}, 2, func() time.Time { return now.Add(2 * time.Minute) })
	require.NoError(t, err)
	require.NoError(t, e.Restore(r.Leases, r.LastKnown))
	assert.Empty(t, e.Tick())
}

func TestStoreRejectsUnsafeReplacementAndReleaseFailure(t *testing.T) {
	for _, kind := range []string{"short-hold", "other-identity", "duplicate-bound", "release-failure", "stale-delete", "disk-full"} {
		t.Run(kind, func(t *testing.T) {
			s, dir, now := storeFixture(t)
			l := storedLease(now, 1)
			storePut(t, s, 1, l)
			m := Mutation{Token: Token{1, 2}, Kind: PutLease, Lease: l}
			switch kind {
			case "short-hold":
				m.Lease.HoldUntil = m.Lease.Expiry
			case "other-identity":
				m.Lease.Identity = "id:other"
			case "duplicate-bound":
				m.Lease.Address = storedLease(now, 2).Address
			case "release-failure":
				m.Kind = DeleteLease
				_, err := s.db.Exec("PRAGMA query_only=ON")
				require.NoError(t, err)
			case "stale-delete":
				m.Kind = DeleteLease
				m.Lease.HoldUntil = m.Lease.HoldUntil.Add(time.Second)
			case "disk-full":
				// SQLite's real page ceiling produces SQLITE_FULL, not a mock error.
				var pages int
				require.NoError(t, s.db.QueryRow("PRAGMA page_count").Scan(&pages))
				_, err := s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages))
				require.NoError(t, err)
				begin := s.begin
				s.begin = func() (leaseTransaction, error) {
					tx, err := begin()
					if err != nil {
						return nil, err
					}
					return fullTransaction{tx}, nil
				}
			}
			require.NoError(t, s.Submit(context.Background(), m))
			result := storeResult(t, s)
			require.Error(t, result.Err)
			if kind == "disk-full" {
				var sqliteErr *sqlite.Error
				require.ErrorAs(t, result.Err, &sqliteErr)
				assert.Equal(t, 13, sqliteErr.Code())
			}
			require.NoError(t, s.Close(context.Background()))
			other, r, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
			require.NoError(t, err)
			defer other.Close(context.Background())
			require.Equal(t, []Lease{l}, r.Leases)
		})
	}
}

type fullTransaction struct{ leaseTransaction }

func (f fullTransaction) Exec(query string, args ...any) (sql.Result, error) {
	// Force allocation of a real page while retaining the real transaction's
	// error/rollback behavior. The test constrains SQLite's page count first.
	return f.leaseTransaction.Exec("CREATE TABLE disk_full_fixture (payload BLOB)")
}

func TestStoreWriterStallIsBoundedAndDNSContinues(t *testing.T) {
	s, dir, now := storeFixture(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	begin := s.begin
	s.begin = func() (leaseTransaction, error) {
		tx, err := begin()
		return faultTransaction{leaseTransaction: tx, before: func() error { close(entered); <-unblock; return nil }}, err
	}
	e, err := NewEngine(fixtureSettings(), 1, func() time.Time { return now })
	require.NoError(t, err)
	r := client(1, Discover)
	offer := offered(t, e, r)
	r.Type, r.RequestedIP, r.ServerID = RequestMessage, offer.Address, netip.MustParseAddr("192.0.2.2")
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, s.Submit(ctx, *out.Mutation))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer not entered")
	}
	var once sync.Once
	unblockWriter := func() { once.Do(func() { close(unblock) }) }
	defer unblockWriter()
	cancel() // Accepted work must commit despite the request disappearing.
	assert.Error(t, e.Apply(fixtureSettings(), 2))
	for i := 0; i < 64; i++ {
		require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, uint64(100 + i)}, Kind: PutLease, Lease: storedLease(now, i+10)}))
	}
	for i := 0; i < 10000; i++ {
		require.ErrorIs(t, s.Submit(context.Background(), Mutation{Token: Token{1, 1000}, Kind: PutLease, Lease: storedLease(now, 100)}), ErrStoreBusy)
	}
	select {
	case <-s.Results():
		t.Fatal("ACK completion before commit")
	default:
	}
	assert.Empty(t, e.DurableLeases())
	assertDNSFixtureAnswers(t)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer closeCancel()
	require.ErrorIs(t, s.Close(closeCtx), context.DeadlineExceeded)
	unblockWriter()
	require.NoError(t, s.Close(context.Background()))
	other, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	defer other.Close(context.Background())
	require.Len(t, recovery.Leases, 1)
	assert.Equal(t, out.Mutation.Lease, recovery.Leases[0])
}

func assertDNSFixtureAnswers(t *testing.T) {
	t.Helper()
	server, err := transport.New(transport.Options{SmallSlots: 8, LargeSlots: 1, Workers: 1}, transport.HandlerFunc(func(_ context.Context, r *transport.Request, out []byte) (int, error) {
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 3}, 1232)
	}))
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.ServeUDP(ctx, conn) }()
	t.Cleanup(func() { cancel(); conn.Close(); <-done })
	client, err := net.Dial("udp4", conn.LocalAddr().String())
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetDeadline(time.Now().Add(time.Second)))
	query := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0, 0, 1, 0, 1}
	_, err = client.Write(query)
	require.NoError(t, err)
	var response [512]byte
	n, err := client.Read(response[:])
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 12)
	assert.Equal(t, byte(3), response[3]&15)
	assert.Equal(t, query[:2], response[:2])
}

func TestStoreProcessCrash(t *testing.T) {
	if point := os.Getenv("DIMSUM_STORE_CRASH"); point != "" {
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		s, _, err := OpenLeaseStore(os.Getenv("DIMSUM_STORE_DIR"), 1024, func() time.Time { return now })
		require.NoError(t, err)
		e, err := NewEngine(fixtureSettings(), 1, func() time.Time { return now })
		require.NoError(t, err)
		r := client(1, Discover)
		offer := offered(t, e, r)
		r.Type, r.RequestedIP, r.ServerID = RequestMessage, offer.Address, netip.MustParseAddr("192.0.2.2")
		out := e.Handle(r)
		require.Nil(t, out.Reply)
		begin := s.begin
		s.begin = func() (leaseTransaction, error) { tx, err := begin(); return crashTransaction{tx, point}, err }
		require.NoError(t, s.Submit(context.Background(), *out.Mutation))
		result := storeResult(t, s)
		require.NoError(t, result.Err)
		require.Equal(t, ACK, e.CompleteCommit(result).Reply.Type)
		os.Exit(23)
	}
	for _, point := range []string{"before", "after", "acked"} {
		t.Run(point, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "dhcp")
			cmd := exec.Command(os.Args[0], "-test.run=^TestStoreProcessCrash$")
			cmd.Env = append(os.Environ(), "DIMSUM_STORE_CRASH="+point, "DIMSUM_STORE_DIR="+dir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.Equal(t, 23, exit.ExitCode(), string(output))
			s, r, err := OpenLeaseStore(dir, 1024, func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) })
			require.NoError(t, err)
			defer s.Close(context.Background())
			if point == "before" {
				assert.Empty(t, r.Leases)
			} else {
				require.Len(t, r.Leases, 1)
				assert.Equal(t, "192.0.2.100", r.Leases[0].Address.String())
			}
		})
	}
}

type crashTransaction struct {
	leaseTransaction
	point string
}

func (c crashTransaction) Commit() error {
	if c.point == "before" {
		os.Exit(23)
	}
	err := c.leaseTransaction.Commit()
	if err == nil && c.point == "after" {
		os.Exit(23)
	}
	return err
}

func TestStoreClockRollbackDuringWrite(t *testing.T) {
	s, dir, now := storeFixture(t)
	var nanos atomic.Int64
	nanos.Store(now.UnixNano())
	s.clock = func() time.Time { return time.Unix(0, nanos.Load()) }
	storePut(t, s, 1, storedLease(now, 1))
	nanos.Store(now.Add(-time.Second).UnixNano())
	require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, 2}, Kind: PutLease, Lease: storedLease(now, 2)}))
	require.ErrorContains(t, storeResult(t, s).Err, "clock")
	require.NoError(t, s.Close(context.Background()))
	other, r, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	defer other.Close(context.Background())
	require.Len(t, r.Leases, 1)
	assert.Equal(t, now, r.LastKnown)
}

func TestStoreAdmissionBoundsAndExclusiveOwnership(t *testing.T) {
	s, dir, now := storeFixture(t)
	other, _, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	if other != nil {
		other.Close(context.Background())
	}
	require.Error(t, err)
	m := Mutation{Token: Token{1, 1}, Kind: PutLease, Lease: storedLease(now, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, s.Submit(ctx, m), context.Canceled)
	large := m
	large.Lease.Identity = string(make([]byte, 259))
	require.Error(t, s.Submit(context.Background(), large))
	large = m
	large.Lease.Hostname = string(make([]byte, 64))
	require.Error(t, s.Submit(context.Background(), large))
	require.NoError(t, s.Submit(context.Background(), m))
	require.Error(t, s.Submit(context.Background(), m))
	require.NoError(t, storeResult(t, s).Err)
	require.Error(t, s.Submit(context.Background(), m))
}

func TestStoreCapacityAndGroupCommit(t *testing.T) {
	for _, capacity := range []int{1024, 4096} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			dir := filepath.Join(t.TempDir(), "dhcp")
			s, _, err := OpenLeaseStore(dir, capacity, func() time.Time { return now })
			require.NoError(t, err)
			defer s.Close(context.Background())
			var transactions atomic.Int64
			begin := s.begin
			s.begin = func() (leaseTransaction, error) { transactions.Add(1); return begin() }
			for start := 0; start < capacity; start += 32 {
				for i := start; i < start+32; i++ {
					l := storedLease(now, i+1)
					if capacity == 4096 {
						l.Identity = "id:" + fmt.Sprintf("%0255d", i+1)
						l.Hostname = strings.Repeat("h", 63)
					}
					require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, uint64(i + 1)}, Kind: PutLease, Lease: l}))
				}
				for i := 0; i < 32; i++ {
					require.NoError(t, storeResult(t, s).Err)
				}
			}
			assert.Less(t, transactions.Load(), int64(capacity/2), "burst writes should share durable transactions")
			require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, uint64(capacity + 1)}, Kind: PutLease, Lease: storedLease(now, capacity+1)}))
			require.Error(t, storeResult(t, s).Err)
			require.NoError(t, s.Close(context.Background()))
			other, r, err := OpenLeaseStore(dir, capacity, func() time.Time { return now })
			require.NoError(t, err)
			defer other.Close(context.Background())
			require.Len(t, r.Leases, capacity)
		})
	}
}

func TestStoreBatchRollbackHasNoPartialOwnership(t *testing.T) {
	s, dir, now := storeFixture(t)
	// Conflicting Bound identities must roll back the whole group, including
	// its earlier valid puts and timestamp update.
	var batch []Mutation
	for i := 1; i <= 32; i++ {
		l := storedLease(now, i)
		l.Identity = "id:same"
		batch = append(batch, Mutation{Token: Token{1, uint64(i)}, Kind: PutLease, Lease: l})
	}
	certain, err := s.commit(batch)
	require.True(t, certain)
	require.Error(t, err)
	require.NoError(t, s.Close(context.Background()))
	other, r, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	defer other.Close(context.Background())
	assert.Empty(t, r.Leases)
}

func TestStoreWALCeilingWithPinnedReader(t *testing.T) {
	s, _, now := storeFixture(t)
	for i := 1; i <= 32; i++ {
		require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, uint64(i)}, Kind: PutLease, Lease: storedLease(now, i)}))
	}
	for i := 0; i < 32; i++ {
		require.NoError(t, storeResult(t, s).Err)
	}
	reader, err := sql.Open("sqlite", s.path)
	require.NoError(t, err)
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	tx, err := reader.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	var count int
	require.NoError(t, tx.QueryRow("SELECT count(*) FROM leases").Scan(&count))
	sequence := uint64(32)
	var failed bool
	for round := 1; round <= 1024; round++ {
		for i := 1; i <= 32; i++ {
			l := storedLease(now, i)
			l.Expiry = l.Expiry.Add(time.Duration(round) * time.Second)
			l.HoldUntil = l.HoldUntil.Add(time.Duration(round) * time.Second)
			sequence++
			require.NoError(t, s.Submit(context.Background(), Mutation{Token: Token{1, sequence}, Kind: PutLease, Lease: l}))
		}
		for i := 0; i < 32; i++ {
			if storeResult(t, s).Err != nil {
				failed = true
			}
		}
		info, err := os.Stat(s.path + "-wal")
		require.NoError(t, err)
		require.LessOrEqual(t, info.Size(), int64(8<<20))
		if failed {
			break
		}
	}
	require.True(t, failed, "a pinned reader must eventually stop admission at the WAL budget")
	require.ErrorContains(t, s.Err(), "WAL budget")
	assertDNSFixtureAnswers(t)
	require.NoError(t, tx.Rollback())
}

func TestStoreExpiredHoldRequiresDurableDelete(t *testing.T) {
	s, dir, now := storeFixture(t)
	l := storedLease(now, 1)
	storePut(t, s, 1, l)
	require.NoError(t, s.Close(context.Background()))
	later := now.Add(2 * time.Hour)
	other, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return later })
	require.NoError(t, err)
	defer other.Close(context.Background())
	require.Equal(t, []Lease{l}, recovery.Leases)
	e, err := NewEngine(Settings{MaxLeases: 1024}, 2, func() time.Time { return later })
	require.NoError(t, err)
	require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
	mutations := e.Tick()
	require.Len(t, mutations, 1)
	assert.Equal(t, []Lease{l}, e.DurableLeases())
	require.NoError(t, other.Submit(context.Background(), mutations[0]))
	result := storeResult(t, other)
	require.NoError(t, result.Err)
	assert.Nil(t, e.CompleteCommit(result).Reply)
	assert.Empty(t, e.DurableLeases())
	require.NoError(t, other.Close(context.Background()))
	final, recovered, err := OpenLeaseStore(dir, 1024, func() time.Time { return later })
	require.NoError(t, err)
	defer final.Close(context.Background())
	assert.Empty(t, recovered.Leases)
	assert.Equal(t, later, recovered.LastKnown)
}

func storeResult(t *testing.T, s *LeaseStore) CommitResult {
	t.Helper()
	select {
	case r, ok := <-s.Results():
		require.True(t, ok, "writer results closed before completion")
		require.NotZero(t, r.Token.Generation)
		require.NotZero(t, r.Token.Sequence)
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not complete")
		return CommitResult{}
	}
}

// A commit error can be ambiguous even if the underlying COMMIT succeeded.
type faultTransaction struct {
	leaseTransaction
	before func() error
	after  error
}

func (f faultTransaction) Commit() error {
	if f.before != nil {
		if err := f.before(); err != nil {
			return err
		}
	}
	if err := f.leaseTransaction.Commit(); err != nil {
		return err
	}
	return f.after
}

func TestStoreCrashBoundaries(t *testing.T) {
	for _, point := range []string{"before-commit", "after-commit-before-ACK", "after-ACK", "uncertain-commit"} {
		t.Run(point, func(t *testing.T) {
			s, dir, now := storeFixture(t)
			e, err := NewEngine(fixtureSettings(), 1, func() time.Time { return now })
			require.NoError(t, err)
			r := client(1, Discover)
			r.ClientID = string([]byte{0, 255, 1})
			offer := offered(t, e, r)
			r.Type, r.RequestedIP, r.ServerID = RequestMessage, offer.Address, netip.MustParseAddr("192.0.2.2")
			out := e.Handle(r)
			require.NotNil(t, out.Mutation)
			assert.Nil(t, out.Reply)
			if point == "before-commit" {
				// Fail SQL itself: this is a definitely rolled-back transaction.
				_, err = s.db.Exec("PRAGMA query_only=ON")
				require.NoError(t, err)
			}
			if point == "uncertain-commit" {
				begin := s.begin
				s.begin = func() (leaseTransaction, error) {
					tx, err := begin()
					return faultTransaction{leaseTransaction: tx, after: errors.New("lost commit confirmation")}, err
				}
			}
			require.NoError(t, s.Submit(context.Background(), *out.Mutation))
			if point == "uncertain-commit" {
				require.Eventually(t, func() bool { return errors.Is(s.Err(), ErrStoreUncertain) }, time.Second, time.Millisecond)
				select {
				case <-s.Results():
					t.Fatal("uncertainty must not release pending ownership")
				default:
				}
			} else {
				result := storeResult(t, s)
				assert.Equal(t, out.Mutation.Token, result.Token)
				if point == "before-commit" {
					require.Error(t, result.Err)
					assert.Nil(t, e.CompleteCommit(result).Reply)
				} else {
					require.NoError(t, result.Err)
					if point == "after-ACK" {
						require.Equal(t, ACK, e.CompleteCommit(result).Reply.Type)
					}
				}
			}
			require.NoError(t, s.Close(context.Background()))
			reopened, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
			require.NoError(t, err)
			defer reopened.Close(context.Background())
			if point == "before-commit" {
				assert.Empty(t, recovery.Leases)
				return
			}
			require.Len(t, recovery.Leases, 1)
			assert.Equal(t, out.Mutation.Lease, recovery.Leases[0])
			fresh, err := NewEngine(fixtureSettings(), 2, func() time.Time { return now })
			require.NoError(t, err)
			require.NoError(t, fresh.Restore(recovery.Leases, recovery.LastKnown))
			assert.Equal(t, out.Mutation.Lease.Address, fresh.Leases()[0].Address)
		})
	}
}

func TestStoreFailsClosedOnStateLoss(t *testing.T) {
	for _, damage := range []string{"missing-db", "corrupt-db", "missing-marker", "version", "rollback", "nonempty-directory"} {
		t.Run(damage, func(t *testing.T) {
			s, dir, now := storeFixture(t)
			if damage == "version" {
				_, err := s.db.Exec("PRAGMA user_version=99")
				require.NoError(t, err)
			}
			require.NoError(t, s.Close(context.Background()))
			switch damage {
			case "missing-db":
				require.NoError(t, os.Remove(filepath.Join(dir, "leases.sqlite")))
			case "corrupt-db":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "leases.sqlite"), []byte("broken"), 0600))
			case "missing-marker":
				require.NoError(t, os.Remove(filepath.Join(dir, "initialized")))
			case "rollback":
				now = now.Add(-time.Second)
			case "nonempty-directory":
				dir = t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "unrelated"), nil, 0600))
			}
			other, _, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
			if other != nil {
				other.Close(context.Background())
			}
			require.Error(t, err)
		})
	}
}
