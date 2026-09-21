package integration_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardLifecycle(t *testing.T) {
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var request dnswire.Message
		if dnswire.ParseRequest(r.Wire, &request) != nil {
			return testutil.Response{Drop: true}
		}
		p := make([]byte, 65535)
		n, e := dnswire.BuildReply(p, &request, dnswire.Reply{Null: true, TTL: 60, RecursionAvailable: true}, 1232)
		if e != nil {
			return testutil.Response{Drop: true}
		}
		p = p[:n]
		p[3] |= 0x20
		p[13] = 'a'
		return testutil.Response{Wire: p}
	})
	require.NoError(t, err)
	defer u.Close()
	c := config.Default()
	c.DNS.Listen = []string{"127.0.0.1:0"}
	c.Admin.Listen = "127.0.0.1:0"
	c.DNS.Upstreams = []string{u.Address()}
	s := new(app.Service)
	require.NoError(t, s.StartForwarding(context.Background(), c))
	defer s.Close()
	assert.True(t, s.Ready())
	address := s.Addresses().DNS[0]
	q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'A', 0, 0, 1, 0, 1}
	for _, network := range []string{"udp", "tcp"} {
		conn, err := net.DialTimeout(network, address, time.Second)
		require.NoError(t, err)
		require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
		wire := q
		if network == "tcp" {
			wire = append([]byte{0, byte(len(q))}, q...)
		}
		_, err = conn.Write(wire)
		require.NoError(t, err)
		out := make([]byte, 65535)
		var n int
		if network == "tcp" {
			var size [2]byte
			_, err = io.ReadFull(conn, size[:])
			require.NoError(t, err)
			n = int(binary.BigEndian.Uint16(size[:]))
			_, err = io.ReadFull(conn, out[:n])
		} else {
			n, err = conn.Read(out)
		}
		require.NoError(t, err)
		conn.Close()
		var m dnswire.Message
		require.NoError(t, dnswire.ScanMessage(out[:n], &m))
		assert.EqualValues(t, 0x1234, m.Question.Header.ID)
		assert.Equal(t, byte('A'), m.Question.Name.Wire[1])
		assert.Zero(t, m.Question.Header.Flags&dnswire.FlagAD)
		assert.EqualValues(t, 1, m.Question.Header.Answers)
	}
	require.NoError(t, s.Close())
	assert.False(t, s.Ready())
	for _, network := range []string{"tcp", "udp"} {
		if network == "tcp" {
			l, e := net.Listen(network, address)
			require.NoError(t, e)
			l.Close()
		} else {
			l, e := net.ListenPacket(network, address)
			require.NoError(t, e)
			l.Close()
		}
	}
}

func TestForwardShutdownCancelsActiveExchange(t *testing.T) {
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, e)
	defer u.Close()
	c := config.Default()
	c.DNS.Listen = []string{"127.0.0.1:0"}
	c.Admin.Listen = "127.0.0.1:0"
	c.DNS.Upstreams = []string{u.Address()}
	s := new(app.Service)
	require.NoError(t, s.StartForwarding(context.Background(), c))
	defer s.Close()
	conn, e := net.Dial("udp", s.Addresses().DNS[0])
	require.NoError(t, e)
	defer conn.Close()
	_, e = conn.Write([]byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1})
	require.NoError(t, e)
	select {
	case <-u.Requests():
	case <-time.After(time.Second):
		require.FailNow(t, "forwarder never sent query")
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case e = <-done:
		assert.NoError(t, e)
	case <-time.After(500 * time.Millisecond):
		require.FailNow(t, "shutdown waited for upstream deadline")
	}
	assert.False(t, s.Ready())
	assert.NoError(t, s.Err())
}

func TestForwardRDZeroDoesNotContactUpstream(t *testing.T) {
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, e)
	defer u.Close()
	c := config.Default()
	c.DNS.Listen = []string{"127.0.0.1:0"}
	c.Admin.Listen = "127.0.0.1:0"
	c.DNS.Upstreams = []string{u.Address()}
	s := new(app.Service)
	require.NoError(t, s.StartForwarding(context.Background(), c))
	defer s.Close()
	conn, e := net.Dial("udp", s.Addresses().DNS[0])
	require.NoError(t, e)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
	_, e = conn.Write([]byte{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'A', 0, 0, 1, 0, 1})
	require.NoError(t, e)
	p := make([]byte, 512)
	n, e := conn.Read(p)
	require.NoError(t, e)
	var m dnswire.Message
	require.NoError(t, dnswire.ScanMessage(p[:n], &m))
	assert.EqualValues(t, 5, m.RCode)
	assert.Empty(t, u.Requests())
}
