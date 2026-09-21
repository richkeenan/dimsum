package testutil_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/testutil"
)

func TestUpstreamScriptedUDPAndTCP(t *testing.T) {
	clock := testutil.NewClock(time.Unix(100, 0))
	u, err := testutil.NewUpstream(clock, func(r testutil.Request) testutil.Response {
		if bytes.Equal(r.Wire, []byte("drop")) {
			return testutil.Response{Drop: true}
		}
		return testutil.Response{Wire: []byte("answer"), Delay: time.Minute}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for _, network := range []string{"udp", "tcp"} {
		c, err := net.Dial(network, u.Address())
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		wire := []byte("query")
		if network == "tcp" {
			wire = append([]byte{0, 5}, wire...)
		}
		if _, err = c.Write(wire); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-u.Requests():
			if r.Network != network || string(r.Wire) != "query" {
				t.Fatalf("unexpected request: %+v", r)
			}
		case <-time.After(time.Second):
			t.Fatal("no request")
		}
		clock.Advance(time.Minute)
		buf := make([]byte, 6)
		if network == "tcp" {
			var size [2]byte
			if _, err = io.ReadFull(c, size[:]); err != nil {
				t.Fatal(err)
			}
			if binary.BigEndian.Uint16(size[:]) != 6 {
				t.Fatal("bad framing")
			}
			_, err = io.ReadFull(c, buf)
		} else {
			_, err = c.Read(buf)
		}
		if err != nil || string(buf) != "answer" {
			t.Fatalf("response %q: %v", buf, err)
		}
		c.Close()
	}
	if !clock.Now().Equal(time.Unix(220, 0)) {
		t.Fatal("time not controlled")
	}
	c, err := net.Dial("udp", u.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("drop"))
	<-u.Requests()
	c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	var b [1]byte
	if _, err = c.Read(b[:]); err == nil {
		t.Fatal("drop returned data")
	}
}

func TestUpstreamCloseCancelsDelayAndIdleTCP(t *testing.T) {
	clock := testutil.NewClock(time.Unix(0, 0))
	u, err := testutil.NewUpstream(clock, func(testutil.Request) testutil.Response { return testutil.Response{Delay: time.Hour} })
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	udp, err := net.Dial("udp", u.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	tcp, err := net.Dial("tcp", u.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp.Write([]byte("query"))
	<-u.Requests()
	done := make(chan struct{})
	go func() { u.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fixture leaked goroutine")
	}
	if clock.Pending() != 0 {
		t.Fatal("fixture leaked timer")
	}
}
