package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stall an admitted writer operation before forwarding it to real SQLite. The
// underlying store lock remains owned, and shutdown must let this accepted
// operation finish rather than treating cancellation as a durable rollback.
type stalledDHCPWriter struct {
	inner            *dhcp.LeaseStore
	entered, release chan struct{}
	results          chan dhcp.CommitResult
	worker           sync.WaitGroup
}

func (w *stalledDHCPWriter) Submit(ctx context.Context, m dhcp.Mutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.worker.Add(1)
	go func() {
		defer w.worker.Done()
		close(w.entered)
		<-w.release
		result := dhcp.CommitResult{Token: m.Token, Err: w.inner.Submit(context.Background(), m)}
		if result.Err == nil {
			var ok bool
			result, ok = <-w.inner.Results()
			if !ok {
				result = dhcp.CommitResult{Token: m.Token, Err: errors.New("writer closed")}
			}
		}
		w.results <- result
	}()
	return nil
}
func (w *stalledDHCPWriter) Results() <-chan dhcp.CommitResult { return w.results }
func (w *stalledDHCPWriter) Err() error                        { return w.inner.Err() }
func (w *stalledDHCPWriter) Close(ctx context.Context) error {
	w.worker.Wait()
	return w.inner.Close(ctx)
}

type appDHCPPacketLink struct {
	appDHCPLink
	packets chan []byte
}

func (l *appDHCPPacketLink) Receive(b []byte) (int, netip.AddrPort, bool, error) {
	select {
	case p := <-l.packets:
		return copy(b, p), netip.MustParseAddrPort("0.0.0.0:68"), false, nil
	case <-l.done:
		return 0, netip.AddrPort{}, false, errors.New("closed")
	}
}

func TestDHCPTimedOutDisableRecoversLatestEnabledGeneration(t *testing.T) {
	for _, changedDomain := range []bool{false, true} {
		t.Run(fmt.Sprint(changedDomain), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dhcp")
			db, _, err := dhcp.OpenLeaseStore(path, 1024, nil)
			require.NoError(t, err)
			mac := [6]byte{2, 0, 0, 0, 0, 1}
			now := time.Now().UTC()
			old := dhcp.Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: netip.MustParseAddr("192.0.2.100"), Expiry: now.Add(10 * time.Minute), HoldUntil: now.Add(20 * time.Minute), State: dhcp.Bound}
			require.NoError(t, db.Submit(context.Background(), dhcp.Mutation{Token: dhcp.Token{Generation: 1, Sequence: 1}, Kind: dhcp.PutLease, Lease: old}))
			result, ok := <-db.Results()
			require.True(t, ok)
			require.NoError(t, result.Err)
			require.NoError(t, db.Close(context.Background()))
			s := NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer func() { unblock(); _ = s.Close(context.Background()) }()
			opens := 0
			s.openStore = func(path string) (dhcp.LeaseWriter, dhcp.LeaseRecovery, error) {
				store, rows, err := dhcp.OpenLeaseStore(path, dhcp.MaximumLeases, nil)
				if err != nil {
					return nil, rows, err
				}
				opens++
				if opens == 1 {
					return &stalledDHCPWriter{inner: store, entered: entered, release: release, results: make(chan dhcp.CommitResult, 1)}, rows, nil
				}
				return store, rows, nil
			}
			link := &appDHCPPacketLink{appDHCPLink: appDHCPLink{done: make(chan struct{})}, packets: make(chan []byte, 1)}
			s.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
				if opens == 0 {
					return link, func(context.Context, netip.Addr) (bool, error) { return false, nil }, nil
				}
				return fixtureDHCPLink(dhcp.Settings{})
			}
			enabled := dhcpSettings()
			require.NoError(t, s.Reconcile(context.Background(), enabled, 1))
			original := s.runtime
			request := make([]byte, 244)
			request[0] = 1
			request[1] = 1
			request[2] = 6
			binary.BigEndian.PutUint32(request[4:8], 42)
			copy(request[12:16], old.Address.AsSlice())
			copy(request[28:34], mac[:])
			copy(request[236:], []byte{99, 130, 83, 99, 53, 1, 3, 255})
			link.packets <- request
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("renewal not admitted")
			}
			disabled := enabled
			disabled.Enabled = false
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			require.ErrorIs(t, s.Reconcile(ctx, disabled, 2), context.DeadlineExceeded)
			assert.Nil(t, s.View())
			assert.False(t, s.Projection().Enabled)
			if changedDomain {
				enabled.LocalDomain = "other.arpa"
			}
			ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel2()
			require.Error(t, s.Reconcile(ctx2, enabled, 3))
			assert.Equal(t, 1, opens)
			other, _, err := dhcp.OpenLeaseStore(path, 1024, nil)
			require.Error(t, err)
			require.Nil(t, other)
			unblock()
			select {
			case <-original.Done():
			case <-time.After(time.Second):
				t.Fatal("writer did not finish closing")
			}
			require.NoError(t, s.Reconcile(context.Background(), enabled, 3))
			assert.Equal(t, "running", s.Status().State)
			assert.EqualValues(t, 3, s.Status().AppliedGeneration)
			assert.Equal(t, 2, opens)
			rows := s.Projection().Leases
			require.Len(t, rows, 1)
			assert.Equal(t, old.Identity, rows[0].Identity)
			assert.True(t, rows[0].Expiry.After(old.Expiry))
			assert.True(t, rows[0].HoldUntil.After(old.HoldUntil))
			assert.Equal(t, enabled.LocalDomain, s.View().Settings().LocalDomain)
		})
	}
}

func dhcpSettings() dhcp.Settings {
	return dhcp.Settings{Enabled: true, Interface: "fixture0", ServerIP: "192.0.2.2", Subnet: "192.0.2.0/24", Gateway: "192.0.2.1", RangeStart: "192.0.2.100", RangeEnd: "192.0.2.110", LeaseSeconds: 3600, LocalDomain: "home.arpa"}
}

type appDHCPLink struct {
	done chan struct{}
	once sync.Once
}

func (l *appDHCPLink) Receive([]byte) (int, netip.AddrPort, bool, error) {
	<-l.done
	return 0, netip.AddrPort{}, false, errors.New("closed")
}
func (l *appDHCPLink) Close() error                               { l.once.Do(func() { close(l.done) }); return nil }
func (l *appDHCPLink) Send(context.Context, dhcp.WireReply) error { return nil }
func (l *appDHCPLink) MTU() int                                   { return 1500 }
func fixtureDHCPLink(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
	return &appDHCPLink{done: make(chan struct{})}, func(context.Context, netip.Addr) (bool, error) { return false, nil }, nil
}

func TestDHCPReconcileRetainsOwnershipAcrossDisableAndFailedEnable(t *testing.T) {
	dir := t.TempDir()
	db, _, err := dhcp.OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
	require.NoError(t, err)
	mac := [6]byte{2, 0, 0, 0, 0, 1}
	lease := dhcp.Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: netip.MustParseAddr("192.0.2.100"), Expiry: time.Now().Add(time.Hour).UTC(), HoldUntil: time.Now().Add(2 * time.Hour).UTC(), State: dhcp.Bound}
	require.NoError(t, db.Submit(context.Background(), dhcp.Mutation{Token: dhcp.Token{Generation: 1, Sequence: 1}, Kind: dhcp.PutLease, Lease: lease}))
	select {
	case result, ok := <-db.Results():
		require.True(t, ok)
		require.NoError(t, result.Err)
	case <-time.After(time.Second):
		t.Fatal("no commit")
	}
	require.NoError(t, db.Close(context.Background()))
	s := NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
	s.openLink = fixtureDHCPLink
	defer s.Close(context.Background())
	enabled := dhcpSettings()
	require.NoError(t, s.Reconcile(context.Background(), enabled, 1))
	require.Len(t, s.Projection().Leases, 1)
	next := enabled
	next.LeaseSeconds = 120
	require.NoError(t, s.Reconcile(context.Background(), next, 2))
	assert.EqualValues(t, 2, s.Projection().Generation)
	bad := next
	bad.Reservations = []dhcp.Reservation{{ID: "other", MAC: "02:00:00:00:00:02", Address: "192.0.2.100"}}
	require.Error(t, s.Reconcile(context.Background(), bad, 3))
	assert.EqualValues(t, 2, s.Status().AppliedGeneration)
	assert.Equal(t, lease, s.Projection().Leases[0])
	topology := next
	topology.LocalDomain = "other.arpa"
	require.Error(t, s.Reconcile(context.Background(), topology, 4))
	disabled := next
	disabled.Enabled = false
	require.NoError(t, s.Reconcile(context.Background(), disabled, 5))
	assert.False(t, s.Projection().Enabled)
	// Disabled edits must not discard old ownership, even across a failed restore.
	disabled.ServerIP = "192.0.2.100"
	disabled.RangeStart = "192.0.2.101"
	require.NoError(t, s.Reconcile(context.Background(), disabled, 6))
	disabled.Enabled = true
	require.Error(t, s.Reconcile(context.Background(), disabled, 7))
	assert.EqualValues(t, 6, s.Status().AppliedGeneration)
	disabled.Enabled = false
	require.NoError(t, s.Reconcile(context.Background(), disabled, 8))
	require.NoError(t, s.Reconcile(context.Background(), enabled, 9))
	require.Len(t, s.Projection().Leases, 1)
	assert.Equal(t, lease, s.Projection().Leases[0])
}

func TestDHCPFailuresLeaveDNSAndAdminServing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	store, err := config.OpenStore(context.Background(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	service := new(Service)
	require.NoError(t, service.StartManaged(context.Background(), store))
	defer service.Close()
	for _, failure := range []string{"bind", "permission", "storage"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			supervisor := NewDHCPSupervisor(root, []string{"0.0.0.0:53"})
			service.mu.Lock()
			previous := service.dhcp
			service.dhcp = supervisor
			service.mu.Unlock()
			defer func() { service.mu.Lock(); service.dhcp = previous; service.mu.Unlock() }()
			supervisor.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) { return nil, nil, errors.New(failure) }
			if failure == "storage" {
				supervisor.openLink = fixtureDHCPLink
				require.NoError(t, os.Mkdir(filepath.Join(root, "dhcp"), 0700))
			}
			require.Error(t, supervisor.Reconcile(context.Background(), dhcpSettings(), 1))
			assert.Equal(t, "error", service.DHCPStatus().State)
			defer supervisor.Close(context.Background())
			assert.True(t, service.Ready())
			assert.NoError(t, service.Err())
			q := new(dns.Msg)
			q.SetQuestion("1.0.0.10.in-addr.arpa.", dns.TypePTR)
			answer, _, err := (&dns.Client{Timeout: time.Second}).Exchange(q, service.Addresses().DNS[0])
			require.NoError(t, err)
			assert.Equal(t, dns.RcodeNameError, answer.Rcode)
			response, err := (&http.Client{Timeout: time.Second}).Get("http://" + service.Addresses().Admin + "/health/ready")
			require.NoError(t, err)
			defer response.Body.Close()
			assert.Equal(t, http.StatusOK, response.StatusCode)
		})
	}
}

func TestDHCPDisabledCreatesNoResourcesAndFailureIsLatched(t *testing.T) {
	dir := t.TempDir()
	s := NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
	calls := 0
	s.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		calls++
		return nil, nil, errors.New("permission denied")
	}
	require.NoError(t, s.Reconcile(context.Background(), dhcp.Settings{}, 1))
	assert.Equal(t, 0, calls)
	_, err := os.Stat(filepath.Join(dir, "dhcp"))
	assert.True(t, os.IsNotExist(err))
	err = s.Reconcile(context.Background(), dhcpSettings(), 2)
	require.ErrorContains(t, err, "permission denied")
	status := s.Status()
	assert.Equal(t, "error", status.State)
	assert.EqualValues(t, 2, status.DesiredGeneration)
	assert.EqualValues(t, 1, status.AppliedGeneration)
	_ = s.Reconcile(context.Background(), dhcpSettings(), 2)
	assert.Equal(t, 1, calls, "failed preparation must not spin workers")
	disabled := dhcpSettings()
	disabled.Enabled = false
	require.NoError(t, s.Reconcile(context.Background(), disabled, 3))
	assert.Equal(t, "disabled", s.Status().State)
	assert.False(t, s.Projection().Enabled)
}

func TestDHCPRejectsDNSUnavailableBeforeOpening(t *testing.T) {
	for _, address := range []string{"127.0.0.1:1053", "[::]:53"} {
		s := NewDHCPSupervisor(t.TempDir(), []string{address})
		var opened atomic.Bool
		s.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
			opened.Store(true)
			return nil, nil, errors.New("opened socket without verified IPv4 DNS")
		}
		require.Error(t, s.Reconcile(context.Background(), dhcpSettings(), 1))
		assert.Equal(t, "error", s.Status().State)
		assert.False(t, opened.Load())
	}
}

func TestDHCPStalledPreparationIsOwnedAndShutdownBounded(t *testing.T) {
	s := NewDHCPSupervisor(t.TempDir(), []string{"0.0.0.0:53"})
	entered, release := make(chan struct{}), make(chan struct{})
	s.openLink = func(settings dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		close(entered)
		<-release
		return fixtureDHCPLink(settings)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Reconcile(ctx, dhcpSettings(), 1) }()
	<-entered
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("stalled preparation blocked caller beyond deadline")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer closeCancel()
	require.ErrorIs(t, s.Close(closeCtx), context.DeadlineExceeded)
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer retryCancel()
	require.Error(t, s.Reconcile(retryCtx, dhcpSettings(), 2))
	close(release)
	require.NoError(t, s.Close(context.Background()))
	assert.False(t, s.Projection().Enabled)
	_, err := os.Stat(filepath.Join(s.dir, "leases.sqlite"))
	assert.True(t, os.IsNotExist(err))
}

// A caller's waiting deadline must not discard healthy preparation or latch a
// failure for the desired generation. Shutdown still fences late preparation.
func TestDHCPPreparationOutlivesCallerButNotSupervisor(t *testing.T) {
	for _, stage := range []string{"link", "store"} {
		for _, shutdown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shutdown-%t", stage, shutdown), func(t *testing.T) {
				s := NewDHCPSupervisor(t.TempDir(), []string{"0.0.0.0:53"})
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(func() { unblock(); _ = s.Close(context.Background()) })
				var links, stores atomic.Int32
				wait := func() { close(entered); <-release }
				s.openLink = func(settings dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
					links.Add(1)
					if stage == "link" {
						wait()
					}
					return fixtureDHCPLink(settings)
				}
				openStore := s.openStore
				s.openStore = func(path string) (dhcp.LeaseWriter, dhcp.LeaseRecovery, error) {
					stores.Add(1)
					store, recovery, err := openStore(path)
					if stage == "store" {
						wait()
					}
					return store, recovery, err
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- s.Reconcile(ctx, dhcpSettings(), 1) }()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("preparation did not start")
				}
				cancel()
				select {
				case err := <-done:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(time.Second):
					t.Fatal("caller did not stop waiting")
				}
				if shutdown {
					// A canceled close returns promptly but permanently fences startup.
					require.ErrorIs(t, s.Close(ctx), context.Canceled)
				}
				unblock()
				if shutdown {
					require.NoError(t, s.Close(context.Background()))
					assert.False(t, s.Status().AppliedEnabled)
					assert.Nil(t, s.View())
				} else {
					require.NoError(t, s.Reconcile(context.Background(), dhcpSettings(), 1))
					assert.Equal(t, "running", s.Status().State)
					assert.EqualValues(t, 1, s.Status().AppliedGeneration)
					assert.EqualValues(t, 1, stores.Load(), "healthy preparation must not be reopened")
					require.NoError(t, s.Close(context.Background()))
				}
				assert.EqualValues(t, 1, links.Load(), "only one preparation owns the gate")
				if stores.Load() != 0 {
					reopened, _, err := dhcp.OpenLeaseStore(s.dir, dhcp.MaximumLeases, nil)
					require.NoError(t, err, "late preparation must release the writer on shutdown")
					require.NoError(t, reopened.Close(context.Background()))
				}
			})
		}
	}
}

func TestDHCPReconcileRecognizesCompletedOwnerSwitch(t *testing.T) {
	s := NewDHCPSupervisor(t.TempDir(), []string{"0.0.0.0:53"})
	s.openLink = fixtureDHCPLink
	defer s.Close(context.Background())
	settings := dhcpSettings()
	require.NoError(t, s.Reconcile(context.Background(), settings, 1))
	settings.LeaseSeconds = 120
	// Models a caller timing out exactly as the owner completes its boundary.
	require.NoError(t, s.runtime.Apply(context.Background(), settings, 2))
	require.NoError(t, s.Reconcile(context.Background(), settings, 2))
	assert.EqualValues(t, 2, s.Status().AppliedGeneration)
}

type recoveringDHCPLink struct {
	appDHCPPacketLink
	failNext atomic.Bool
	replies  chan dhcp.WireReply
}

func (l *recoveringDHCPLink) Send(ctx context.Context, reply dhcp.WireReply) error {
	if l.failNext.Swap(false) {
		return errors.New("temporary send failure")
	}
	select {
	case l.replies <- reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDHCPTransientSendFailureAllowsApplyAndRecovers(t *testing.T) {
	s := NewDHCPSupervisor(t.TempDir(), []string{"0.0.0.0:53"})
	link := &recoveringDHCPLink{
		appDHCPPacketLink: appDHCPPacketLink{appDHCPLink: appDHCPLink{done: make(chan struct{})}, packets: make(chan []byte, 1)},
		replies:           make(chan dhcp.WireReply, 1),
	}
	link.failNext.Store(true)
	s.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		return link, func(context.Context, netip.Addr) (bool, error) { return false, nil }, nil
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	settings := dhcpSettings()
	require.NoError(t, s.Reconcile(context.Background(), settings, 1))
	// INFORM exercises reply delivery without depending on a lease/probe timer.
	request := make([]byte, 244)
	request[0], request[1], request[2] = 1, 1, 6
	binary.BigEndian.PutUint32(request[4:8], 42)
	copy(request[12:16], []byte{192, 0, 2, 100})
	copy(request[28:34], []byte{2, 0, 0, 0, 0, 1})
	copy(request[236:], []byte{99, 130, 83, 99, 53, 1, 8, 255})
	link.packets <- request
	require.Eventually(t, func() bool {
		return s.Status().Runtime.Error == "temporary send failure"
	}, time.Second, time.Millisecond)
	assert.Equal(t, "degraded", s.Status().State)

	settings.LeaseSeconds = 120
	require.NoError(t, s.Reconcile(context.Background(), settings, 2), "a recoverable egress error must not block configuration")
	assert.EqualValues(t, 2, s.Status().AppliedGeneration)
	assert.EqualValues(t, 120, s.View().Settings().LeaseSeconds)
	assert.Equal(t, "degraded", s.Status().State, "applying settings is not proof that delivery recovered")
	link.packets <- request
	select {
	case <-link.replies:
	case <-time.After(time.Second):
		t.Fatal("reply delivery did not recover")
	}
	require.Eventually(t, func() bool { return s.Status().State == "running" }, time.Second, time.Millisecond)
	assert.Empty(t, s.Status().LastError)
	assert.Empty(t, s.Status().Runtime.Error)

	// Losing ingress is terminal, unlike a dropped reply. It must still block
	// configuration even after earlier sends recovered successfully.
	require.NoError(t, link.Close())
	select {
	case <-s.runtime.Done():
	case <-time.After(time.Second):
		t.Fatal("failed ingress did not stop runtime")
	}
	require.Error(t, s.Reconcile(context.Background(), settings, 3))
	assert.EqualValues(t, 2, s.Status().AppliedGeneration)
	assert.Equal(t, "degraded", s.Status().State)
}

func TestDHCPShutdownFencesSuccessfulPublication(t *testing.T) {
	for _, stage := range []string{"prepared", "applied"} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/timeout-%t", stage, timeout), func(t *testing.T) {
				dir := t.TempDir()
				db, _, err := dhcp.OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
				require.NoError(t, err)
				mac := [6]byte{2, 0, 0, 0, 0, 1}
				now := time.Now().UTC()
				lease := dhcp.Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: netip.MustParseAddr("192.0.2.100"), Expiry: now.Add(time.Minute), HoldUntil: now.Add(time.Minute), State: dhcp.Bound}
				require.NoError(t, db.Submit(context.Background(), dhcp.Mutation{Token: dhcp.Token{Generation: 1, Sequence: 1}, Kind: dhcp.PutLease, Lease: lease}))
				result, ok := <-db.Results()
				require.True(t, ok)
				require.NoError(t, result.Err)
				require.NoError(t, db.Close(context.Background()))
				db, recovery, err := dhcp.OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
				require.NoError(t, err)
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				writer := &stalledDHCPWriter{inner: db, entered: entered, release: release, results: make(chan dhcp.CommitResult, 1)}
				link := &appDHCPPacketLink{appDHCPLink: appDHCPLink{done: make(chan struct{})}, packets: make(chan []byte, 1)}
				s := NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
				settings := dhcpSettings()
				rt, err := dhcp.StartRuntime(context.Background(), settings, 1, link, func(context.Context, netip.Addr) (bool, error) { return false, nil }, writer, recovery)
				require.NoError(t, err)
				s.runtime = rt
				// Hold the real reconciliation gate at exactly the final publication
				// boundary: runtime installed, or owner Apply succeeded, but not published.
				s.gate <- struct{}{}
				var gateOnce sync.Once
				releaseGate := func() { gateOnce.Do(func() { <-s.gate }) }
				defer func() { unblock(); releaseGate(); _ = s.Close(context.Background()) }()
				generation := uint64(1)
				previousGeneration := uint64(0)
				if stage == "applied" {
					require.NoError(t, s.publishApplied(settings, 1))
					generation = 2
					previousGeneration = 1
					settings.LeaseSeconds = 7200
					require.NoError(t, rt.Apply(context.Background(), settings, generation))
				}
				s.mu.Lock()
				s.status.DesiredGeneration = generation
				s.status.DesiredEnabled = true
				s.status.PendingGeneration = generation
				s.mu.Unlock()
				request := make([]byte, 244)
				request[0] = 1
				request[1] = 1
				request[2] = 6
				binary.BigEndian.PutUint32(request[4:8], 42)
				copy(request[12:16], lease.Address.AsSlice())
				copy(request[28:34], mac[:])
				copy(request[236:], []byte{99, 130, 83, 99, 53, 1, 3, 255})
				link.packets <- request
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("writer did not enter pending commit")
				}
				closeCtx := context.Background()
				cancel := func() {}
				if timeout {
					closeCtx, cancel = context.WithTimeout(closeCtx, 30*time.Millisecond)
				}
				defer cancel()
				closed := make(chan error, 1)
				go func() { closed <- s.Close(closeCtx) }()
				require.Eventually(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.closed }, time.Second, time.Millisecond)
				if timeout {
					require.ErrorIs(t, <-closed, context.DeadlineExceeded)
				}
				// Resume the previously successful operation only after Close's monotonic
				// boundary, including after a timed-out Close has already returned.
				assert.Error(t, s.publishApplied(settings, generation))
				assert.False(t, s.Status().AppliedEnabled)
				assert.NotEqual(t, "running", s.Status().State)
				assert.Equal(t, previousGeneration, s.Status().AppliedGeneration)
				assert.Nil(t, s.View())
				assert.False(t, s.Projection().Enabled)
				releaseGate()
				unblock()
				if !timeout {
					require.NoError(t, <-closed)
				} else {
					require.NoError(t, s.Close(context.Background()))
				}
				assert.Nil(t, s.View())
				assert.False(t, s.Status().AppliedEnabled)
			})
		}
	}
}
