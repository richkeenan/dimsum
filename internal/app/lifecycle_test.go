package app_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() config.Config {
	c := config.Default()
	c.DNS.Listen = []string{"127.0.0.1:0"}
	c.Admin.Listen = "127.0.0.1:0"
	return c
}

func TestLifecycleCancellationAndDuplicate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := new(app.Service)
	require.NoError(t, s.Start(ctx, testConfig()))
	t.Cleanup(func() { s.Close() })
	assert.Error(t, s.Start(ctx, testConfig()), "duplicate startup accepted")
	addresses := s.Addresses()
	require.Len(t, addresses.DNS, 1)
	assert.NotEqual(t, "127.0.0.1:0", addresses.DNS[0], "missing bound address")
	cancel()
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		require.FailNow(t, "cancellation leaked lifecycle goroutine")
	}
	for _, a := range append(addresses.DNS, addresses.Admin) {
		l, err := net.Listen("tcp", a)
		require.NoError(t, err, "listener leaked")
		l.Close()
	}
	u, err := net.ListenPacket("udp", addresses.DNS[0])
	require.NoError(t, err, "UDP leaked")
	u.Close()
}

func TestLifecycleInvalidAndPartialBindRollback(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := reserved.Addr().String()
	reserved.Close()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()
	for _, admin := range []string{"not-an-address", busy.Addr().String()} {
		c := testConfig()
		c.DNS.Listen = []string{address}
		c.Admin.Listen = admin
		s := new(app.Service)
		t.Cleanup(func() { s.Close() })
		require.Error(t, s.Start(context.Background(), c), "invalid/busy bind accepted")
		l, err := net.Listen("tcp", address)
		require.NoError(t, err, "TCP rollback failed")
		l.Close()
		u, err := net.ListenPacket("udp", address)
		require.NoError(t, err, "UDP rollback failed")
		u.Close()
		// Failure does not poison a service that never started.
		require.NoError(t, s.Start(context.Background(), testConfig()))
		s.Close()
	}
}

func TestLifecycleUDPConflictRollsBackTCP(t *testing.T) {
	u, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer u.Close()
	c := testConfig()
	c.DNS.Listen = []string{u.LocalAddr().String()}
	s := new(app.Service)
	t.Cleanup(func() { s.Close() })
	require.Error(t, s.Start(context.Background(), c), "UDP conflict ignored")
	l, err := net.Listen("tcp", u.LocalAddr().String())
	require.NoError(t, err, "TCP leaked after UDP conflict")
	l.Close()
}

func TestLifecycleSecondServiceCannotDisruptFirst(t *testing.T) {
	s := new(app.Service)
	require.NoError(t, s.Start(context.Background(), testConfig()))
	defer s.Close()
	c := testConfig()
	c.DNS.Listen = s.Addresses().DNS
	other := new(app.Service)
	t.Cleanup(func() { other.Close() })
	require.Error(t, other.Start(context.Background(), c), "duplicate service bound same port")
	conn, err := net.DialTimeout("tcp", s.Addresses().DNS[0], time.Second)
	require.NoError(t, err, "existing service disrupted")
	conn.Close()
}

func TestLifecycleCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := new(app.Service)
	t.Cleanup(func() { s.Close() })
	assert.Error(t, s.Start(ctx, testConfig()), "cancelled context opened listeners")
}
