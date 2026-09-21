package upstream_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func query() []byte {
	return []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'W', 'w', 'W', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0, 0, 1, 0, 1}
}
func answer(q []byte) []byte {
	p := append([]byte(nil), q...)
	p[2] |= 0x80
	return p
}
func client(t *testing.T, address string, timeout time.Duration) *upstream.Client {
	t.Helper()
	c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(address)}, Timeout: timeout, AttemptTimeout: timeout, MaxOutstanding: 1})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

// Every mutation must be ignored while a later valid datagram is accepted.
func TestExchangeWrongResponses(t *testing.T) {
	for _, mode := range []string{"id", "question", "type", "class", "qr", "opcode", "malformed", "source"} {
		t.Run(mode, func(t *testing.T) {
			u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			require.NoError(t, err)
			defer u.Close()
			rogue, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			require.NoError(t, err)
			defer rogue.Close()
			done := make(chan error, 1)
			go func() {
				buf := make([]byte, 65535)
				u.SetDeadline(time.Now().Add(time.Second))
				n, peer, e := u.ReadFromUDP(buf)
				if e != nil {
					done <- e
					return
				}
				good := answer(buf[:n])
				bad := bytes.Clone(good)
				switch mode {
				case "id":
					bad[0] ^= 1
				case "question":
					bad[13] = 'x'
				case "type":
					bad[len(bad)-3] = 28
				case "class":
					bad[len(bad)-1] = 3
				case "qr":
					bad[2] &^= 0x80
				case "opcode":
					bad[2] |= 8
				case "malformed":
					bad = append(bad, 1)
				}
				bad[3] = 3
				sender := u
				if mode == "source" {
					sender = rogue
				}
				if _, e = sender.WriteToUDP(bad, peer); e == nil {
					_, e = u.WriteToUDP(good, peer)
				}
				done <- e
			}()
			out := bytes.Repeat([]byte{0xcc}, 65535)
			result, err := client(t, u.LocalAddr().String(), time.Second).Exchange(context.Background(), query(), out)
			require.NoError(t, err)
			require.NoError(t, <-done)
			var m dnswire.Message
			require.NoError(t, dnswire.ScanMessage(out[:result.N], &m))
			assert.Zero(t, m.RCode)
		})
	}
}

func TestExchangeLossCancellationAndBound(t *testing.T) {
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, err)
	defer u.Close()
	c := client(t, u.Address(), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := c.Exchange(ctx, query(), make([]byte, 65535)); done <- e }()
	select {
	case <-u.Requests():
	case <-time.After(time.Second):
		require.FailNow(t, "request not sent")
	}
	out := bytes.Repeat([]byte{0xcc}, 65535)
	_, err = c.Exchange(context.Background(), query(), out)
	assert.ErrorIs(t, err, upstream.ErrOverloaded)
	cancel()
	select {
	case err = <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.FailNow(t, "cancellation stalled")
	}
	start := time.Now()
	_, err = c.Exchange(context.Background(), query(), out)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	assert.Equal(t, bytes.Repeat([]byte{0xcc}, 65535), out, "unvalidated output escaped")
	assert.Zero(t, c.Outstanding())
}

func TestExchangeTCPRetry(t *testing.T) {
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		p := answer(r.Wire)
		if r.Network == "udp" {
			p[2] |= 2
		}
		return testutil.Response{Wire: p}
	})
	require.NoError(t, err)
	defer u.Close()
	out := make([]byte, 65535)
	result, err := client(t, u.Address(), time.Second).Exchange(context.Background(), query(), out)
	require.NoError(t, err)
	assert.True(t, result.TCP)
	assert.Equal(t, 2, result.Attempts)
	assert.Zero(t, binary.BigEndian.Uint16(out[2:4])&dnswire.FlagTC)
	assert.Equal(t, "udp", (<-u.Requests()).Network)
	assert.Equal(t, "tcp", (<-u.Requests()).Network)
}

func TestExchangeFailoverAndAttemptBudget(t *testing.T) {
	for _, mode := range []string{"loss", "servfail", "refused", "nxdomain", "nodata"} {
		t.Run(mode, func(t *testing.T) {
			first, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
				p := answer(r.Wire)
				switch mode {
				case "loss":
					return testutil.Response{Drop: true}
				case "servfail":
					p[3] = 2
				case "refused":
					p[3] = 5
				case "nxdomain":
					p[3] = 3
				}
				return testutil.Response{Wire: p}
			})
			require.NoError(t, e)
			defer first.Close()
			second, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return testutil.Response{Wire: answer(r.Wire)} })
			require.NoError(t, e)
			defer second.Close()
			c, e := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(first.Address()), netip.MustParseAddrPort(second.Address())}, Timeout: time.Second, AttemptTimeout: 30 * time.Millisecond})
			require.NoError(t, e)
			result, e := c.Exchange(context.Background(), query(), make([]byte, 65535))
			require.NoError(t, e)
			if mode == "nxdomain" || mode == "nodata" {
				assert.Equal(t, 1, result.Attempts)
				assert.Equal(t, first.Address(), result.Endpoint.String())
			} else {
				assert.Equal(t, 2, result.Attempts)
				assert.Equal(t, second.Address(), result.Endpoint.String())
			}
		})
	}
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, e)
	defer u.Close()
	endpoint := netip.MustParseAddrPort(u.Address())
	c, e := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{endpoint, endpoint, endpoint, endpoint}, Timeout: time.Second, AttemptTimeout: 20 * time.Millisecond})
	require.NoError(t, e)
	_, e = c.Exchange(context.Background(), query(), make([]byte, 65535))
	assert.Error(t, e)
	assert.Len(t, u.Requests(), 3, "attempts exceeded hard cap")
}

func TestExchangeTCPValidationAndCancellation(t *testing.T) {
	for _, mode := range []string{"id", "question", "class", "malformed", "tc", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
				p := answer(r.Wire)
				if r.Network == "udp" {
					p[2] |= 2
				} else {
					switch mode {
					case "id":
						p[0] ^= 1
					case "question":
						p[13] = 'x'
					case "class":
						p[len(p)-1] = 3
					case "malformed":
						p = append(p, 0)
					case "tc":
						p[2] |= 2
					case "cancel", "deadline":
						return testutil.Response{Drop: true}
					}
				}
				return testutil.Response{Wire: p}
			})
			require.NoError(t, e)
			defer u.Close()
			c := client(t, u.Address(), 100*time.Millisecond)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := bytes.Repeat([]byte{0xcc}, 65535)
			done := make(chan error, 1)
			go func() { _, e := c.Exchange(ctx, query(), out); done <- e }()
			for _, network := range []string{"udp", "tcp"} {
				select {
				case r := <-u.Requests():
					assert.Equal(t, network, r.Network)
				case <-time.After(time.Second):
					require.FailNow(t, "missing retry")
				}
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case e = <-done:
				assert.Error(t, e)
				if mode == "cancel" {
					assert.ErrorIs(t, e, context.Canceled)
				}
			case <-time.After(time.Second):
				require.FailNow(t, "exchange leaked")
			}
			assert.Equal(t, bytes.Repeat([]byte{0xcc}, 65535), out)
			assert.Zero(t, c.Outstanding())
		})
	}
}

func TestExchangeLateDuplicateEntropyAndSocketRelease(t *testing.T) {
	u, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, e)
	defer u.Close()
	type observed struct {
		peer *net.UDPAddr
		id   uint16
		err  error
	}
	seen := make(chan observed, 32)
	go func() {
		var previous []byte
		buf := make([]byte, 65535)
		for range 24 {
			u.SetDeadline(time.Now().Add(2 * time.Second))
			n, peer, err := u.ReadFromUDP(buf)
			if err != nil {
				seen <- observed{err: err}
				return
			}
			if previous != nil {
				if _, err = u.WriteToUDP(previous, peer); err != nil {
					seen <- observed{err: err}
					return
				}
			}
			p := answer(buf[:n])
			_, err = u.WriteToUDP(p, peer)
			seen <- observed{peer: peer, id: binary.BigEndian.Uint16(buf), err: err}
			previous = bytes.Clone(p)
			previous[3] = 3
		}
	}()
	c := client(t, u.LocalAddr().String(), time.Second)
	ports := map[int]bool{}
	ids := map[uint16]bool{}
	for range 24 {
		out := make([]byte, 65535)
		result, e := c.Exchange(context.Background(), query(), out)
		require.NoError(t, e)
		obs := <-seen
		require.NoError(t, obs.err)
		assert.False(t, ids[obs.id], "quarantined ID reused")
		ids[obs.id] = true
		ports[obs.peer.Port] = true
		assert.Zero(t, out[3]&15, "late duplicate accepted")
		assert.Equal(t, len(query()), result.N)
		// Exchange return transfers no live socket ownership to the caller.
		rebound, e := net.ListenUDP("udp", obs.peer)
		require.NoError(t, e, "upstream socket leaked")
		rebound.Close()
	}
	assert.Greater(t, len(ports), 8, "source port entropy missing")
	assert.Zero(t, c.Outstanding())
}

func TestExchangeDeadlineRejectsLateResponse(t *testing.T) {
	clock := testutil.NewClock(time.Now())
	u, e := testutil.NewUpstream(clock, func(r testutil.Request) testutil.Response {
		return testutil.Response{Wire: answer(r.Wire), Delay: time.Second}
	})
	require.NoError(t, e)
	defer u.Close()
	c := client(t, u.Address(), 30*time.Millisecond)
	out := bytes.Repeat([]byte{0xcc}, 65535)
	done := make(chan error, 1)
	go func() { _, e := c.Exchange(context.Background(), query(), out); done <- e }()
	select {
	case <-u.Requests():
	case <-time.After(time.Second):
		require.FailNow(t, "no request")
	}
	select {
	case e = <-done:
		assert.ErrorIs(t, e, context.DeadlineExceeded)
	case <-time.After(time.Second):
		require.FailNow(t, "deadline failed")
	}
	clock.Advance(time.Second)
	assert.Equal(t, bytes.Repeat([]byte{0xcc}, 65535), out)
	assert.Zero(t, c.Outstanding())
}

func TestExchangeLargeQueryUsesTCP(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("large.example.", dns.TypeA)
	q.SetEdns0(4096, true)
	q.IsEdns0().Option = []dns.EDNS0{&dns.EDNS0_LOCAL{Code: 65001, Data: make([]byte, 2000)}}
	wire, e := q.Pack()
	require.NoError(t, e)
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return testutil.Response{Wire: answer(r.Wire)} })
	require.NoError(t, e)
	defer u.Close()
	result, e := client(t, u.Address(), time.Second).Exchange(context.Background(), wire, make([]byte, 65535))
	require.NoError(t, e)
	assert.True(t, result.TCP)
	assert.Equal(t, 1, result.Attempts)
	assert.Equal(t, "tcp", (<-u.Requests()).Network)
}

func TestExchangeTCPPartialFramesAndSocketRelease(t *testing.T) {
	for _, mode := range []string{"success", "partial", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			tcp, e := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			require.NoError(t, e)
			defer tcp.Close()
			udp, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: tcp.Addr().(*net.TCPAddr).Port})
			require.NoError(t, e)
			defer udp.Close()
			connected := make(chan struct{})
			worker := make(chan error, 1)
			go func() {
				worker <- func() error {
					udp.SetDeadline(time.Now().Add(time.Second))
					buf := make([]byte, 65535)
					n, peer, e := udp.ReadFromUDP(buf)
					if e != nil {
						return e
					}
					p := answer(buf[:n])
					p[2] |= 2
					if _, e = udp.WriteToUDP(p, peer); e != nil {
						return e
					}
					tcp.SetDeadline(time.Now().Add(time.Second))
					conn, e := tcp.Accept()
					if e != nil {
						return e
					}
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(time.Second))
					var size [2]byte
					if _, e = io.ReadFull(conn, size[:]); e != nil {
						return e
					}
					n = int(binary.BigEndian.Uint16(size[:]))
					if _, e = io.ReadFull(conn, buf[:n]); e != nil {
						return e
					}
					close(connected)
					if mode == "success" {
						frame := append(size[:], answer(buf[:n])...)
						for _, b := range frame {
							if _, e = conn.Write([]byte{b}); e != nil {
								return e
							}
						}
					}
					if mode == "partial" {
						if _, e = conn.Write(size[:1]); e != nil {
							return e
						}
					}
					_, e = conn.Read(buf[:1])
					if e != io.EOF {
						return fmt.Errorf("upstream TCP not closed: %v", e)
					}
					return nil
				}()
			}()
			c := client(t, tcp.Addr().String(), 150*time.Millisecond)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, e := c.Exchange(ctx, query(), make([]byte, 65535)); result <- e }()
			select {
			case <-connected:
			case <-time.After(time.Second):
				require.FailNow(t, "no TCP retry")
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case e = <-result:
				if mode == "success" {
					assert.NoError(t, e)
				} else if mode == "cancel" {
					assert.ErrorIs(t, e, context.Canceled)
				} else {
					assert.ErrorIs(t, e, context.DeadlineExceeded)
				}
			case <-time.After(time.Second):
				require.FailNow(t, "exchange stalled")
			}
			// Healthy sockets now remain leased to the pool until owner shutdown.
			if mode == "success" {
				require.NoError(t, c.Close())
			}
			select {
			case e = <-worker:
				assert.NoError(t, e)
			case <-time.After(time.Second):
				require.FailNow(t, "TCP socket leaked")
			}
			assert.Zero(t, c.Outstanding())
		})
	}
}

func TestExchangeCancelledUDPSocketRelease(t *testing.T) {
	u, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, e)
	defer u.Close()
	c := client(t, u.LocalAddr().String(), time.Second)
	for range 16 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, e := c.Exchange(ctx, query(), make([]byte, 65535)); done <- e }()
		require.NoError(t, u.SetDeadline(time.Now().Add(time.Second)))
		_, peer, e := u.ReadFromUDP(make([]byte, 65535))
		cancel()
		require.NoError(t, e)
		select {
		case e = <-done:
			assert.ErrorIs(t, e, context.Canceled)
		case <-time.After(time.Second):
			require.FailNow(t, "cancellation stalled")
		}
		socket, e := net.ListenUDP("udp", peer)
		require.NoError(t, e, "cancelled UDP socket leaked")
		socket.Close()
	}
	assert.Zero(t, c.Outstanding())
}
