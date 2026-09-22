package dhcp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

const (
	storeQueue    = 64
	storeBatch    = 32
	storeDelay    = 5 * time.Millisecond
	storeDBBytes  = 4 << 20
	storeWALBytes = 8 << 20
	// cache_spill is disabled: one transaction writes at most one frame per
	// database page. Reserve the entire bounded database plus frame headers.
	storeMaxFrames      = 1024*(4096+24) + 32
	storeMetadataSchema = `CREATE TABLE metadata (singleton INTEGER PRIMARY KEY CHECK(singleton=1), id TEXT NOT NULL, last_known INTEGER NOT NULL CHECK(last_known>0))`
	storeLeaseSchema    = `CREATE TABLE leases (address TEXT PRIMARY KEY, identity BLOB NOT NULL CHECK(length(identity) BETWEEN 4 AND 258), mac BLOB NOT NULL CHECK(length(mac)=6), hostname TEXT NOT NULL CHECK(length(hostname)<=63), expiry INTEGER NOT NULL CHECK(expiry>0), hold_until INTEGER NOT NULL CHECK(hold_until>=expiry), state TEXT NOT NULL CHECK(state IN ('bound','quarantined'))) WITHOUT ROWID`
	storeIdentitySchema = `CREATE UNIQUE INDEX bound_identity ON leases(identity) WHERE state='bound'`
)

var (
	ErrStoreBusy      = errors.New("dhcp: lease writer queue full")
	ErrStoreClosed    = errors.New("dhcp: lease store closed")
	ErrStoreUncertain = errors.New("dhcp: lease commit uncertain; close and recover before resuming")
)

// LeaseRecovery is a bounded startup snapshot, not a live database reader.
// Pass it to Engine.Restore before opening packet service. Never omit expired
// rows: their HoldUntil and durable deletion barrier still apply.
type LeaseRecovery struct {
	Leases    []Lease
	LastKnown time.Time
}

type leaseTransaction interface {
	Exec(string, ...any) (sql.Result, error)
	Commit() error
	Rollback() error
}

// LeaseStore is independent of query-history storage and all DNS lifecycle
// channels. One worker owns one SQLite connection. All limits are internal.
// Only Results with nil Err authorize Engine.CompleteCommit success.
type LeaseStore struct {
	db         *sql.DB
	marker     *os.File
	path       string
	capacity   int
	clock      func() time.Time
	begin      func() (leaseTransaction, error)
	lastKnown  time.Time // worker-owned after startup
	queue      chan Mutation
	results    chan CommitResult
	stop, done chan struct{}
	mu         sync.Mutex
	closed     bool
	err        error
	lastToken  Token
	closeErr   error
}

// OpenLeaseStore takes an exclusively owned DHCP state directory (for example
// data_dir/dhcp). Empty initialization is allowed ONLY if this call creates
// that directory. Missing/partial state in an existing directory fails closed.
// The injected clock must be concurrency-safe. No worker is started on failure.
func OpenLeaseStore(dir string, capacity int, clock func() time.Time) (*LeaseStore, LeaseRecovery, error) {
	var recovery LeaseRecovery
	if capacity < 1 || capacity > 4096 {
		return nil, recovery, fmt.Errorf("dhcp: invalid store capacity")
	}
	if clock == nil {
		clock = time.Now
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, recovery, err
	}
	dir = abs
	fresh := false
	if err = os.Mkdir(dir, 0700); err == nil {
		fresh = true
	} else if !errors.Is(err, os.ErrExist) {
		return nil, recovery, err
	}
	flags := os.O_RDWR
	if fresh {
		flags |= os.O_CREATE | os.O_EXCL
	}
	marker, err := os.OpenFile(filepath.Join(dir, "initialized"), flags, 0600)
	if err != nil {
		return nil, recovery, fmt.Errorf("dhcp: missing initialization marker; explicit recovery required: %w", err)
	}
	s := &LeaseStore{marker: marker, path: filepath.Join(dir, "leases.sqlite"), capacity: capacity, clock: clock,
		queue: make(chan Mutation, storeQueue), results: make(chan CommitResult, storeQueue), stop: make(chan struct{}), done: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			if s.db != nil {
				s.db.Close()
			}
			marker.Close()
		}
	}()
	if err = syscall.Flock(int(marker.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, recovery, fmt.Errorf("dhcp: state directory already in use: %w", err)
	}
	var id string
	if fresh {
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return nil, recovery, err
		}
		id = hex.EncodeToString(random[:])
		if _, err = marker.WriteString("dimsum-dhcp-v1:" + id + "\n"); err != nil {
			return nil, recovery, err
		}
		if err = marker.Sync(); err != nil {
			return nil, recovery, err
		}
		if err = syncStoreDir(dir); err != nil {
			return nil, recovery, err
		}
		if err = syncStoreDir(filepath.Dir(dir)); err != nil {
			return nil, recovery, err
		}
	} else {
		info, statErr := marker.Stat()
		if statErr != nil || info.Size() != 48 {
			return nil, recovery, fmt.Errorf("dhcp: invalid initialization marker")
		}
		data := make([]byte, 48)
		if _, err = marker.ReadAt(data, 0); err != nil {
			return nil, recovery, err
		}
		id = string(data[15:47])
		if _, err = hex.DecodeString(id); err != nil || string(data) != "dimsum-dhcp-v1:"+id+"\n" {
			return nil, recovery, fmt.Errorf("dhcp: invalid initialization marker")
		}
	}
	if !fresh {
		info, e := os.Stat(s.path)
		if e != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > storeDBBytes {
			return nil, recovery, fmt.Errorf("dhcp: missing or oversized lease database; explicit recovery required")
		}
	}
	if err = s.checkWAL(0); err != nil {
		return nil, recovery, err
	}
	u := url.URL{Scheme: "file", Path: s.path}
	q := u.Query()
	q.Set("mode", "rw")
	if fresh {
		q.Set("mode", "rwc")
	}
	q.Set("_txlock", "immediate")
	for _, p := range []string{"busy_timeout=25", "page_size=4096", "journal_mode=WAL", "synchronous=FULL", "cache_size=-512", "mmap_size=0", "cache_spill=OFF", "wal_autocheckpoint=0", "journal_size_limit=0", "max_page_count=1024"} {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	s.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, recovery, err
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	if err = s.db.Ping(); err != nil {
		return nil, recovery, fmt.Errorf("dhcp: open lease database: %w", err)
	}
	if fresh {
		now := clock().UTC()
		if !storeTimeValid(now) {
			return nil, recovery, fmt.Errorf("dhcp: invalid wall clock")
		}
		tx, e := s.db.Begin()
		if e != nil {
			return nil, recovery, e
		}
		defer tx.Rollback()
		_, err = tx.Exec("PRAGMA application_id=1145586512; PRAGMA user_version=1;" + storeMetadataSchema + ";" + storeLeaseSchema + ";" + storeIdentitySchema)
		if err == nil {
			_, err = tx.Exec("INSERT INTO metadata VALUES(1,?,?)", id, now.UnixNano())
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return nil, recovery, fmt.Errorf("dhcp: initialize lease database: %w", err)
		}
		if err = os.Chmod(s.path, 0600); err != nil {
			return nil, recovery, err
		}
		if err = syncStoreDir(dir); err != nil {
			return nil, recovery, err
		}
	}
	if recovery, err = s.recover(id); err != nil {
		return nil, recovery, err
	}
	s.lastKnown = recovery.LastKnown
	s.begin = func() (leaseTransaction, error) { return s.db.Begin() }
	ok = true
	go s.run()
	return s, recovery, nil
}

func syncStoreDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func storeTimeValid(t time.Time) bool { return t.UnixNano() > 0 && time.Unix(0, t.UnixNano()).Equal(t) }

func (s *LeaseStore) recover(id string) (LeaseRecovery, error) {
	var r LeaseRecovery
	for pragma, want := range map[string]int{"application_id": 1145586512, "user_version": 1, "page_size": 4096, "synchronous": 2, "max_page_count": 1024} {
		var got int
		if err := s.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			return r, fmt.Errorf("dhcp: invalid lease database %s", pragma)
		}
	}
	// Version alone cannot establish the constraints relied on by the writer.
	for name, want := range map[string]string{"metadata": storeMetadataSchema, "leases": storeLeaseSchema, "bound_identity": storeIdentitySchema} {
		var got string
		if err := s.db.QueryRow("SELECT sql FROM sqlite_schema WHERE name=?", name).Scan(&got); err != nil || got != want {
			return r, fmt.Errorf("dhcp: invalid lease schema for %s", name)
		}
	}
	var objects int
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&objects); err != nil || objects != 3 {
		return r, fmt.Errorf("dhcp: unexpected lease schema objects")
	}
	var integrity string
	if err := s.db.QueryRow("PRAGMA integrity_check(1)").Scan(&integrity); err != nil || integrity != "ok" {
		return r, fmt.Errorf("dhcp: lease database integrity failure: %v", err)
	}
	var gotID string
	var last int64
	if err := s.db.QueryRow("SELECT id,last_known FROM metadata WHERE singleton=1").Scan(&gotID, &last); err != nil || gotID != id || last <= 0 {
		return r, fmt.Errorf("dhcp: lease metadata mismatch or corruption")
	}
	r.LastKnown = time.Unix(0, last).UTC()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM leases").Scan(&count); err != nil || count > s.capacity {
		return r, fmt.Errorf("dhcp: lease row capacity exceeded or invalid schema")
	}
	// SQLite affinity is not a strict type guarantee. In particular a TEXT
	// identity and an equal BLOB identity compare differently in the unique
	// index; accepting TEXT here would bypass the bound-identity barrier.
	var invalid int
	if err := s.db.QueryRow(`SELECT count(*) FROM leases WHERE typeof(identity)!='blob' OR typeof(mac)!='blob' OR typeof(address)!='text' OR typeof(hostname)!='text' OR typeof(state)!='text' OR typeof(expiry)!='integer' OR typeof(hold_until)!='integer'`).Scan(&invalid); err != nil || invalid != 0 {
		return r, fmt.Errorf("dhcp: corrupt lease storage types")
	}
	rows, err := s.db.Query("SELECT address,identity,mac,hostname,expiry,hold_until,state FROM leases ORDER BY address")
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Lease
		var address string
		var identity, mac []byte
		var expiry, hold int64
		if err = rows.Scan(&address, &identity, &mac, &l.Hostname, &expiry, &hold, &l.State); err != nil {
			return r, err
		}
		if len(mac) != 6 || expiry <= 0 || hold < expiry {
			return r, fmt.Errorf("dhcp: corrupt lease fields")
		}
		copy(l.MAC[:], mac)
		l.Identity = string(identity)
		l.Address, err = netip.ParseAddr(address)
		if err != nil || l.Address.String() != address {
			return r, fmt.Errorf("dhcp: corrupt lease address")
		}
		l.Expiry, l.HoldUntil = time.Unix(0, expiry).UTC(), time.Unix(0, hold).UTC()
		r.Leases = append(r.Leases, l)
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	e, err := NewEngine(Settings{MaxLeases: s.capacity}, 1, s.clock)
	if err == nil {
		err = e.Restore(r.Leases, r.LastKnown)
	}
	return r, err
}

// Submit never waits for disk or queue space. Context cancellation only governs
// admission: after acceptance the outcome MUST be consumed even if the request
// disappears. Submit engine mutations once, in increasing token order. On an
// admission error the owner can complete that unsubmitted mutation with error.
// A duplicate/stale-token call is a caller bug: never turn its rejection into
// a failure completion for an earlier accepted mutation with the same token.
func (s *LeaseStore) Submit(ctx context.Context, m Mutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(m.Lease.Identity) > 258 || len(m.Lease.Hostname) > 63 {
		return fmt.Errorf("dhcp: mutation exceeds serialized field limits")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if s.err != nil {
		return s.err
	}
	if m.Token.Generation == 0 || m.Token.Sequence == 0 || m.Token.Generation < s.lastToken.Generation || m.Token.Generation == s.lastToken.Generation && m.Token.Sequence <= s.lastToken.Sequence {
		return fmt.Errorf("dhcp: non-increasing mutation token")
	}
	if len(s.queue) == cap(s.queue) {
		return ErrStoreBusy
	}
	// Do not retain a short substring backed by an unbounded packet buffer.
	m.Lease.Identity = strings.Clone(m.Lease.Identity)
	m.Lease.Hostname = strings.Clone(m.Lease.Hostname)
	select {
	case s.queue <- m:
		s.lastToken = m.Token
		return nil
	default:
		return ErrStoreBusy
	}
}

func (s *LeaseStore) Results() <-chan CommitResult { return s.results }

// Err reports DHCP-only degradation. An uncertain batch intentionally has no
// completion: keep its engine entries pending until the store/engine restart.
func (s *LeaseStore) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *LeaseStore) setError(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

// Close stops admission and abandons queued work; stop the packet owner first.
// An in-flight transaction is allowed to finish. Always recover a fresh engine
// on reopening, including when shutdown interrupted completion delivery.
// A timeout does not interrupt fsync or close an in-use connection.
func (s *LeaseStore) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *LeaseStore) run() {
	defer func() { s.closeErr = errors.Join(s.db.Close(), s.marker.Close()); close(s.results); close(s.done) }()
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		var first Mutation
		select {
		case <-s.stop:
			return
		case first = <-s.queue:
		}
		batch := []Mutation{first}
		timer := time.NewTimer(storeDelay)
	collect:
		for len(batch) < storeBatch {
			select {
			case m := <-s.queue:
				batch = append(batch, m)
			case <-timer.C:
				break collect
			case <-s.stop:
				timer.Stop()
				return
			}
		}
		timer.Stop()
		err := s.Err()
		certain := true
		if err == nil {
			certain, err = s.commit(batch)
		}
		if !certain {
			s.setError(errors.Join(ErrStoreUncertain, err))
			continue
		}
		if err != nil {
			s.setError(err)
		}
		for _, m := range batch {
			select {
			case s.results <- CommitResult{Token: m.Token, Err: err}:
			case <-s.stop:
				return
			}
		}
	}
}

func (s *LeaseStore) checkWAL(reserve int64) error {
	info, err := os.Stat(s.path + "-wal")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size()+reserve > storeWALBytes {
		return fmt.Errorf("dhcp: WAL budget exceeded")
	}
	return nil
}

func (s *LeaseStore) commit(batch []Mutation) (bool, error) {
	now := s.clock().UTC()
	if !storeTimeValid(now) || now.Before(s.lastKnown) {
		return true, fmt.Errorf("dhcp: clock precedes durable last-known time")
	}
	if err := s.checkWAL(storeMaxFrames); err != nil {
		return true, err
	}
	tx, err := s.begin()
	if err != nil {
		return true, err
	}
	rollback := func(err error) (bool, error) { e := tx.Rollback(); return e == nil, errors.Join(err, e) }
	for _, m := range batch {
		l := m.Lease
		if m.Kind != PutLease && m.Kind != DeleteLease {
			return rollback(fmt.Errorf("dhcp: invalid mutation kind"))
		}
		e, _ := NewEngine(Settings{MaxLeases: 1}, 1, func() time.Time { return now })
		if err = e.Restore([]Lease{l}, time.Time{}); err != nil {
			return rollback(err)
		}
		if !storeTimeValid(l.Expiry) || !storeTimeValid(l.HoldUntil) {
			return rollback(fmt.Errorf("dhcp: invalid lease timestamps"))
		}
		var result sql.Result
		if m.Kind == PutLease {
			result, err = tx.Exec(`INSERT INTO leases VALUES(?,?,?,?,?,?,?) ON CONFLICT(address) DO UPDATE SET identity=excluded.identity,mac=excluded.mac,hostname=excluded.hostname,expiry=excluded.expiry,hold_until=excluded.hold_until,state=excluded.state WHERE leases.identity=excluded.identity AND leases.hold_until<=excluded.hold_until`, l.Address.String(), []byte(l.Identity), l.MAC[:], l.Hostname, l.Expiry.UnixNano(), l.HoldUntil.UnixNano(), string(l.State))
		} else {
			result, err = tx.Exec("DELETE FROM leases WHERE address=? AND identity=? AND expiry=? AND hold_until=? AND state=?", l.Address.String(), []byte(l.Identity), l.Expiry.UnixNano(), l.HoldUntil.UnixNano(), string(l.State))
		}
		if err != nil {
			return rollback(err)
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil || changed != 1 {
			return rollback(fmt.Errorf("dhcp: mutation conflicts with durable ownership"))
		}
	}
	// Enforce capacity inside the same transaction without a second connection.
	_, err = tx.Exec("UPDATE metadata SET last_known=CASE WHEN (SELECT count(*) FROM leases)<=? THEN ? ELSE NULL END WHERE singleton=1", s.capacity, now.UnixNano())
	if err != nil {
		return rollback(err)
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	s.lastKnown = now
	// A checkpoint failure cannot undo a successful commit. Deliver successes,
	// but degrade subsequent admission; recovery resolves maintenance failures.
	var busy, log, checkpointed int
	if err = s.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		s.setError(fmt.Errorf("dhcp: checkpoint: %w", err))
	}
	return true, nil
}
