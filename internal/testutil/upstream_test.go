package testutil_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func awaitRequest(t *testing.T, u *testutil.Upstream) testutil.Request {
	t.Helper()
	select {
	case r := <-u.Requests():
		return r
	case <-time.After(time.Second):
		require.FailNow(t, "no request notification")
		return testutil.Request{}
	}
}

func TestUpstreamScriptedUDPAndTCP(t *testing.T) {
	clock := testutil.NewClock(time.Unix(100, 0))
	u, err := testutil.NewUpstream(clock, func(r testutil.Request) testutil.Response {
		if bytes.Equal(r.Wire, []byte("drop")) {
			return testutil.Response{Drop: true}
		}
		return testutil.Response{Wire: []byte("answer"), Delay: time.Minute}
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, u.Close()) })
	for _, network := range []string{"udp", "tcp"} {
		c, err := net.Dial(network, u.Address())
		require.NoError(t, err)
		t.Cleanup(func() { c.Close() })
		require.NoError(t, c.SetDeadline(time.Now().Add(time.Second)))
		wire := []byte("query")
		if network == "tcp" {
			wire = append([]byte{0, 5}, wire...)
		}
		_, err = c.Write(wire)
		require.NoError(t, err)
		r := awaitRequest(t, u)
		assert.Equal(t, network, r.Network)
		assert.Equal(t, []byte("query"), r.Wire)
		clock.Advance(time.Minute)
		buf := make([]byte, 6)
		if network == "tcp" {
			var size [2]byte
			_, err = io.ReadFull(c, size[:])
			require.NoError(t, err)
			require.Equal(t, uint16(6), binary.BigEndian.Uint16(size[:]), "bad framing")
			_, err = io.ReadFull(c, buf)
		} else {
			_, err = c.Read(buf)
		}
		require.NoError(t, err)
		assert.Equal(t, "answer", string(buf))
		assert.NoError(t, c.Close())
	}
	assert.True(t, clock.Now().Equal(time.Unix(220, 0)), "time not controlled")
	c, err := net.Dial("udp", u.Address())
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte("drop"))
	require.NoError(t, err)
	assert.Equal(t, []byte("drop"), awaitRequest(t, u).Wire)
	require.NoError(t, c.SetReadDeadline(time.Now().Add(20*time.Millisecond)))
	var b [1]byte
	_, err = c.Read(b[:])
	require.Error(t, err, "drop returned data")
	var timeout net.Error
	require.ErrorAs(t, err, &timeout)
	assert.True(t, timeout.Timeout(), "drop should time out")
}

func TestUpstreamCloseCancelsDelayAndIdleTCP(t *testing.T) {
	clock := testutil.NewClock(time.Unix(0, 0))
	u, err := testutil.NewUpstream(clock, func(testutil.Request) testutil.Response { return testutil.Response{Delay: time.Hour} })
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, u.Close()) })
	udp, err := net.Dial("udp", u.Address())
	require.NoError(t, err)
	defer udp.Close()
	tcp, err := net.Dial("tcp", u.Address())
	require.NoError(t, err)
	defer tcp.Close()
	_, err = udp.Write([]byte("query"))
	require.NoError(t, err)
	assert.Equal(t, []byte("query"), awaitRequest(t, u).Wire)
	done := make(chan error, 1)
	go func() { done <- u.Close() }()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "fixture leaked goroutine")
	}
	assert.Zero(t, clock.Pending(), "fixture leaked timer")
}

func TestUpstreamReportsUDPWriteFailure(t *testing.T) {
	u, err := testutil.NewUpstream(testutil.NewClock(time.Unix(0, 0)), func(testutil.Request) testutil.Response {
		// Larger than any UDP datagram: the local write must fail.
		return testutil.Response{Wire: make([]byte, 65536)}
	})
	require.NoError(t, err)
	t.Cleanup(func() { u.Close() })
	c, err := net.Dial("udp", u.Address())
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte("query"))
	require.NoError(t, err)
	awaitRequest(t, u)
	require.Eventually(t, func() bool { return u.Err() != nil }, time.Second, time.Millisecond)
	err = u.Close()
	require.ErrorContains(t, err, "fixture UDP response")
	var opErr *net.OpError
	assert.ErrorAs(t, err, &opErr)
	assert.Equal(t, err, u.Close(), "repeated close retains worker failure")
}
