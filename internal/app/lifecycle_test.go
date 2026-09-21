package app_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
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
	if err := s.Start(ctx, testConfig()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Start(ctx, testConfig()); err == nil {
		t.Fatal("duplicate startup accepted")
	}
	addresses := s.Addresses()
	if len(addresses.DNS) != 1 || addresses.DNS[0] == "127.0.0.1:0" {
		t.Fatal("missing bound addresses")
	}
	cancel()
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("cancellation leaked lifecycle goroutine")
	}
	for _, a := range append(addresses.DNS, addresses.Admin) {
		l, err := net.Listen("tcp", a)
		if err != nil {
			t.Fatalf("listener leaked: %v", err)
		}
		l.Close()
	}
	u, err := net.ListenPacket("udp", addresses.DNS[0])
	if err != nil {
		t.Fatalf("UDP leaked: %v", err)
	}
	u.Close()
}

func TestLifecycleInvalidAndPartialBindRollback(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	reserved.Close()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	for _, admin := range []string{"not-an-address", busy.Addr().String()} {
		c := testConfig()
		c.DNS.Listen = []string{address}
		c.Admin.Listen = admin
		s := new(app.Service)
		if err := s.Start(context.Background(), c); err == nil {
			s.Close()
			t.Fatal("invalid/busy bind accepted")
		}
		l, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("TCP rollback failed: %v", err)
		}
		l.Close()
		u, err := net.ListenPacket("udp", address)
		if err != nil {
			t.Fatalf("UDP rollback failed: %v", err)
		}
		u.Close()
		// Failure does not poison a service that never started.
		if err := s.Start(context.Background(), testConfig()); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestLifecycleUDPConflictRollsBackTCP(t *testing.T) {
	u, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	c := testConfig()
	c.DNS.Listen = []string{u.LocalAddr().String()}
	s := new(app.Service)
	if err := s.Start(context.Background(), c); err == nil {
		s.Close()
		t.Fatal("UDP conflict ignored")
	}
	l, err := net.Listen("tcp", u.LocalAddr().String())
	if err != nil {
		t.Fatalf("TCP leaked after UDP conflict: %v", err)
	}
	l.Close()
}

func TestLifecycleSecondServiceCannotDisruptFirst(t *testing.T) {
	s := new(app.Service)
	if err := s.Start(context.Background(), testConfig()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := testConfig()
	c.DNS.Listen = s.Addresses().DNS
	other := new(app.Service)
	if err := other.Start(context.Background(), c); err == nil {
		other.Close()
		t.Fatal("duplicate service bound same port")
	}
	conn, err := net.DialTimeout("tcp", s.Addresses().DNS[0], time.Second)
	if err != nil {
		t.Fatal("existing service disrupted", err)
	}
	conn.Close()
}

func TestLifecycleCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := new(app.Service)
	if err := s.Start(ctx, testConfig()); err == nil {
		s.Close()
		t.Fatal("cancelled context opened listeners")
	}
}
