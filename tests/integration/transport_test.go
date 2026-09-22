package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func transportFixture(t *testing.T, opts transport.Options, h transport.Handler) (*transport.Server, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := config.Default()
	c.DNS.Listen = []string{"127.0.0.1:0"}
	c.Admin.Listen = "127.0.0.1:0"
	lifecycle := new(app.Service)
	require.NoError(t, lifecycle.Start(ctx, c))
	s, err := transport.New(opts, h)
	require.NoError(t, err)
	l := lifecycle.Listeners()
	done := make(chan error, 2)
	go func() { done <- s.ServeUDP(ctx, l.UDP[0].(*net.UDPConn)) }()
	go func() { done <- s.ServeTCP(ctx, l.TCP[0]) }()
	t.Cleanup(func() {
		cancel()
		lifecycle.Close()
		for range 2 {
			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Error("transport failed to stop")
			}
		}
	})
	return s, lifecycle.Addresses().DNS[0]
}

func nullHandler(ctx context.Context, r *transport.Request, out []byte) (int, error) {
	return dnswire.BuildReply(out, &r.Message, dnswire.Reply{Null: true, TTL: 2}, 1232)
}
func query(t *testing.T, id uint16) []byte {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion("MiXeD.example.", 1)
	m.Id = id
	p, e := m.Pack()
	require.NoError(t, e)
	return p
}
func frame(p []byte) []byte {
	b := make([]byte, len(p)+2)
	binary.BigEndian.PutUint16(b, uint16(len(p)))
	copy(b[2:], p)
	return b
}
func readFrame(c net.Conn) ([]byte, error) {
	var h [2]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return nil, e
	}
	b := make([]byte, binary.BigEndian.Uint16(h[:]))
	_, e := io.ReadFull(c, b)
	return b, e
}

func TestTransportUDPAndTCPPipeline(t *testing.T) {
	_, addr := transportFixture(t, transport.Options{}, transport.HandlerFunc(nullHandler))
	u, e := net.Dial("udp", addr)
	require.NoError(t, e)
	defer u.Close()
	require.NoError(t, u.SetDeadline(time.Now().Add(time.Second)))
	_, e = u.Write(query(t, 42))
	require.NoError(t, e)
	b := make([]byte, 65535)
	n, e := u.Read(b)
	require.NoError(t, e)
	var m dns.Msg
	require.NoError(t, m.Unpack(b[:n]))
	assert.Equal(t, uint16(42), m.Id)
	assert.Equal(t, "MiXeD.example.", m.Question[0].Name)
	assert.Len(t, m.Answer, 1)
	c, e := net.Dial("tcp", addr)
	require.NoError(t, e)
	defer c.Close()
	require.NoError(t, c.SetDeadline(time.Now().Add(2*time.Second)))
	f := frame(query(t, 1))
	for _, part := range [][]byte{f[:1], f[1:3], f[3:]} {
		_, e = c.Write(part)
		require.NoError(t, e)
	}
	_, e = c.Write(append(frame(query(t, 2)), frame(query(t, 3))...))
	require.NoError(t, e)
	for id := uint16(1); id <= 3; id++ {
		p, e := readFrame(c)
		require.NoError(t, e)
		require.NoError(t, m.Unpack(p))
		assert.Equal(t, id, m.Id)
	}
}

func TestTransportResolverAttachment(t *testing.T) {
	u, e := testutil.NewUpstream(testutil.NewClock(time.Unix(0, 0)), func(r testutil.Request) testutil.Response {
		p := append([]byte(nil), r.Wire...)
		p[2] |= 0x80
		return testutil.Response{Wire: p}
	})
	require.NoError(t, e)
	t.Cleanup(func() { assert.NoError(t, u.Close()) })
	h := transport.HandlerFunc(func(ctx context.Context, r *transport.Request, out []byte) (int, error) {
		d := net.Dialer{}
		c, e := d.DialContext(ctx, "udp", u.Address())
		if e != nil {
			return 0, e
		}
		defer c.Close()
		deadline, _ := ctx.Deadline()
		c.SetDeadline(deadline)
		if _, e = c.Write(r.Wire); e != nil {
			return 0, e
		}
		return c.Read(out)
	})
	_, addr := transportFixture(t, transport.Options{}, h)
	c, e := net.Dial("udp", addr)
	require.NoError(t, e)
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	p := query(t, 123)
	_, e = c.Write(p)
	require.NoError(t, e)
	var b [512]byte
	n, e := c.Read(b[:])
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(b[:n]))
	assert.Equal(t, uint16(123), got.Id)
	assert.True(t, got.Response)
	select {
	case req := <-u.Requests():
		assert.Equal(t, p, req.Wire)
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestTransportExhaustionAndOversizedDatagram(t *testing.T) {
	entered := make(chan []byte, 1)
	release := make(chan struct{})
	ownership := make(chan error, 2)
	h := transport.HandlerFunc(func(ctx context.Context, r *transport.Request, out []byte) (int, error) {
		before := append([]byte(nil), r.Wire...)
		select {
		case entered <- append([]byte(nil), r.Wire...):
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		if !bytes.Equal(before, r.Wire) {
			ownership <- fmt.Errorf("request changed while borrowed")
		} else {
			ownership <- nil
		}
		return nullHandler(ctx, r, out)
	})
	s, addr := transportFixture(t, transport.Options{SmallSlots: 1, LargeSlots: 1, Workers: 1}, h)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	u, e := net.Dial("udp", addr)
	require.NoError(t, e)
	defer u.Close()
	u.SetDeadline(time.Now().Add(time.Second))
	p := query(t, 55)
	_, e = u.Write(p)
	require.NoError(t, e)
	select {
	case got := <-entered:
		assert.Equal(t, p, got)
	case <-time.After(time.Second):
		t.Fatal("handler not entered")
	}
	for range 10 {
		_, e = u.Write(query(t, 66))
		require.NoError(t, e)
	}
	require.Eventually(t, func() bool { return s.Stats().SlotDrops > 0 }, time.Second, time.Millisecond)
	// A valid short query followed by undeclared bytes must not be accepted as a short prefix.
	// Exceed the 2048-byte small slot while staying below macOS's default
	// UDP send limit. This checks large-slot routing, not the host's maximum.
	big := make([]byte, 4096)
	copy(big, p)
	_, e = u.Write(big)
	require.NoError(t, e)
	require.Eventually(t, func() bool { return s.Stats().LargeReceived == 1 }, time.Second, time.Millisecond)
	assert.Equal(t, 2048+65535, s.StorageBytes())
	close(release)
	var buf [65535]byte
	n, e := u.Read(buf[:])
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(buf[:n]))
	assert.Equal(t, uint16(55), got.Id)
	select {
	case e := <-ownership:
		require.NoError(t, e)
	case <-time.After(time.Second):
		t.Fatal("missing ownership result")
	}
	n, e = u.Read(buf[:])
	require.NoError(t, e)
	require.NoError(t, got.Unpack(buf[:n]))
	assert.Equal(t, dns.RcodeFormatError, got.Rcode)
}

func TestTransportReplyMatrixAndBudgets(t *testing.T) {
	h := transport.HandlerFunc(func(ctx context.Context, r *transport.Request, out []byte) (int, error) {
		code := uint16(r.Message.Question.Header.ID)
		if code == 100 {
			var q dns.Msg
			if e := q.Unpack(r.Wire); e != nil {
				return 0, e
			}
			a := new(dns.Msg)
			a.SetReply(&q)
			a.Compress = true
			for range 100 {
				a.Answer = append(a.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: 16, Class: 1}, Txt: []string{"some long response data to force truncation"}})
			}
			b, e := a.Pack()
			return copy(out, b), e
		}
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: code, Null: code == 0}, 1232)
	})
	_, addr := transportFixture(t, transport.Options{}, h)
	for _, network := range []string{"udp", "tcp"} {
		for _, code := range []uint16{0, 3, 5, 100} {
			for _, size := range []uint16{0, 512, 1232, 4096} {
				c, e := net.Dial(network, addr)
				require.NoError(t, e)
				require.NoError(t, c.SetDeadline(time.Now().Add(time.Second)))
				q := new(dns.Msg)
				q.SetQuestion("MiXeD.example.", 1)
				q.Id = code
				if size != 0 {
					q.SetEdns0(size, true)
				}
				p, e := q.Pack()
				require.NoError(t, e)
				if network == "tcp" {
					p = frame(p)
				}
				_, e = c.Write(p)
				require.NoError(t, e)
				var raw []byte
				if network == "tcp" {
					raw, e = readFrame(c)
				} else {
					b := make([]byte, 65535)
					var n int
					n, e = c.Read(b)
					raw = b[:n]
				}
				require.NoError(t, e)
				c.Close()
				var got dns.Msg
				require.NoError(t, got.Unpack(raw))
				assert.Equal(t, code, got.Id)
				assert.Equal(t, "MiXeD.example.", got.Question[0].Name)
				if network == "udp" {
					budget := 512
					if size > 512 {
						budget = 1232
					}
					assert.LessOrEqual(t, len(raw), budget)
				}
				if code == 100 {
					assert.Equal(t, network == "udp", got.Truncated)
					if network == "tcp" {
						assert.Len(t, got.Answer, 100)
					}
				} else {
					assert.Equal(t, int(code), got.Rcode)
				}
			}
		}
	}
}

func TestTransportRequestErrors(t *testing.T) {
	_, addr := transportFixture(t, transport.Options{}, transport.HandlerFunc(nullHandler))
	for _, tc := range []struct {
		name   string
		change func(*dns.Msg)
		code   int
	}{
		{"opcode", func(m *dns.Msg) { m.Opcode = 5 }, 4},
		{"class", func(m *dns.Msg) { m.Question[0].Qclass = 3 }, 5},
		{"transfer", func(m *dns.Msg) { m.Question[0].Qtype = 252 }, 5},
		{"badvers", func(m *dns.Msg) { m.SetEdns0(1232, true); m.IsEdns0().SetVersion(1) }, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := new(dns.Msg)
			m.SetQuestion("MiXeD.example.", 1)
			tc.change(m)
			p, e := m.Pack()
			require.NoError(t, e)
			c, e := net.Dial("udp", addr)
			require.NoError(t, e)
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			_, e = c.Write(p)
			require.NoError(t, e)
			var b [1232]byte
			n, e := c.Read(b[:])
			require.NoError(t, e)
			var got dns.Msg
			require.NoError(t, got.Unpack(b[:n]))
			assert.Equal(t, tc.code, got.Rcode)
		})
	}
}

func TestTransportDeadlineAndCancellation(t *testing.T) {
	h := transport.HandlerFunc(func(ctx context.Context, r *transport.Request, out []byte) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	_, addr := transportFixture(t, transport.Options{RequestTimeout: 20 * time.Millisecond}, h)
	c, e := net.Dial("udp", addr)
	require.NoError(t, e)
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	_, e = c.Write(query(t, 99))
	require.NoError(t, e)
	var b [512]byte
	n, e := c.Read(b[:])
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(b[:n]))
	assert.Equal(t, 2, got.Rcode)
}

func TestTransportQueueConsumesRequestBudget(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	h := transport.HandlerFunc(func(ctx context.Context, r *transport.Request, out []byte) (int, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nullHandler(ctx, r, out)
	})
	s, addr := transportFixture(t, transport.Options{Workers: 1, SmallSlots: 2, RequestTimeout: 20 * time.Millisecond}, h)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	c, e := net.Dial("udp", addr)
	require.NoError(t, e)
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	_, e = c.Write(query(t, 1))
	require.NoError(t, e)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not entered")
	}
	_, e = c.Write(query(t, 2))
	require.NoError(t, e)
	// Give the queued request longer than its entire budget, while the worker is busy.
	time.Sleep(60 * time.Millisecond)
	close(release)
	var b [512]byte
	for range 2 {
		n, e := c.Read(b[:])
		require.NoError(t, e)
		var got dns.Msg
		require.NoError(t, got.Unpack(b[:n]))
		assert.Equal(t, 2, got.Rcode)
	}
	assert.Equal(t, int32(1), calls.Load())
	assert.Zero(t, s.Stats().SlotDrops)
}

func TestTransportSlowReader(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("MiXeD.example.", 1)
	q.Response = true
	for range 240 {
		q.Answer = append(q.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: "MiXeD.example.", Rrtype: 16, Class: 1}, Txt: []string{string(bytes.Repeat([]byte{'x'}, 240))}})
	}
	q.Compress = true
	big, e := q.Pack()
	require.NoError(t, e)
	h := transport.HandlerFunc(func(ctx context.Context, r *transport.Request, out []byte) (int, error) { return copy(out, big), nil })
	s, addr := transportFixture(t, transport.Options{WriteTimeout: 30 * time.Millisecond, ReadTimeout: time.Second, SmallSlots: 1, LargeSlots: 1}, h)
	c, e := net.Dial("tcp", addr)
	require.NoError(t, e)
	defer c.Close()
	require.NoError(t, c.(*net.TCPConn).SetReadBuffer(1024))
	c.SetWriteDeadline(time.Now().Add(time.Second))
	frames := bytes.Repeat(frame(query(t, 1)), 2000)
	_, e = c.Write(frames)
	require.NoError(t, e)
	require.Eventually(t, func() bool { return s.Stats().WriteErrors > 0 }, 3*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return s.Stats().Connections == 0 }, time.Second, time.Millisecond)
}

func TestTransportSlowClientAndConnectionBound(t *testing.T) {
	s, addr := transportFixture(t, transport.Options{MaxConnections: 1, ReadTimeout: 50 * time.Millisecond}, transport.HandlerFunc(nullHandler))
	c, e := net.Dial("tcp", addr)
	require.NoError(t, e)
	defer c.Close()
	_, e = c.Write([]byte{0})
	require.NoError(t, e)
	require.Eventually(t, func() bool { return s.Stats().Connections == 1 }, time.Second, time.Millisecond)
	second, e := net.Dial("tcp", addr)
	require.NoError(t, e)
	defer second.Close()
	second.SetDeadline(time.Now().Add(time.Second))
	var b [1]byte
	_, e = second.Read(b[:])
	assert.Error(t, e)
	require.Eventually(t, func() bool { return s.Stats().ConnectionDrops == 1 }, time.Second, time.Millisecond)
	c.SetDeadline(time.Now().Add(time.Second))
	_, e = c.Read(b[:])
	assert.Error(t, e)
	require.Eventually(t, func() bool { return s.Stats().Connections == 0 }, time.Second, time.Millisecond)
}
