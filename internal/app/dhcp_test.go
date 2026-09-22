package app

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	s := NewDHCPSupervisor(t.TempDir(), []string{"127.0.0.1:1053"})
	s.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		t.Fatal("opened socket without DNS")
		return nil, nil, nil
	}
	require.Error(t, s.Reconcile(context.Background(), dhcpSettings(), 1))
	assert.Equal(t, "error", s.Status().State)
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
