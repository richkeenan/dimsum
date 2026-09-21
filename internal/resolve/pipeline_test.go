package resolve_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/miekg/dns"
	"net/netip"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardRejectHeaderDependentNames(t *testing.T) {
	// CD changes flags byte 3 from zero (root) to 16. With this question,
	// the mutated pointer still decodes as a valid binary label: rescanning
	// alone cannot detect the change in the answer owner's meaning.
	wire := []byte{0x12, 1, 1, 0x10, 0, 1, 0, 0, 0, 0, 0, 0, 6, 'a', 'b', 'c', 'd', 'e', 'f', 0, 0, 1, 0, 1}
	r := transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		p := bytes.Clone(r.Wire)
		binary.BigEndian.PutUint16(p[2:4], 0x8100)
		p[7] = 1
		p = append(p, 0xc0, 3, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 1, 2, 3, 4)
		return testutil.Response{Wire: p}
	})
	require.NoError(t, err)
	defer u.Close()
	c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}})
	require.NoError(t, err)
	n, err := resolve.New(c).Resolve(context.Background(), &r, make([]byte, 65535))
	assert.Error(t, err, "client flag patch changed owner semantics")
	assert.Zero(t, n)
}

func TestForwardRDZeroMiss(t *testing.T) {
	p := resolve.New(nil)
	wire := []byte{0x12, 0x34, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'A', 0, 0, 1, 0, 1}
	r := transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	out := make([]byte, 65535)
	n, err := p.Resolve(context.Background(), &r, out)
	require.NoError(t, err)
	var m dnswire.Message
	require.NoError(t, dnswire.ScanMessage(out[:n], &m))
	assert.EqualValues(t, 5, m.RCode)
	assert.EqualValues(t, 0x1234, m.Question.Header.ID)
}

func TestForwardOptionsFlagsAndOpaqueData(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("MiXeD.example.", 65400)
	q.Id = 1234
	q.CheckingDisabled = true
	q.AuthenticatedData = true
	q.SetEdns0(4096, true)
	q.IsEdns0().Option = []dns.EDNS0{&dns.EDNS0_COOKIE{Code: dns.EDNS0COOKIE, Cookie: "0011223344556677"}, &dns.EDNS0_LOCAL{Code: 8, Data: []byte{0, 1, 0, 0}}, &dns.EDNS0_LOCAL{Code: 65001, Data: []byte{1, 2, 3}}}
	wire, e := q.Pack()
	require.NoError(t, e)
	original := bytes.Clone(wire)
	workerErrors := make(chan error, 1)
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var request dns.Msg
		if err := request.Unpack(r.Wire); err != nil {
			workerErrors <- err
			return testutil.Response{Drop: true}
		}
		reply := new(dns.Msg)
		reply.SetReply(&request)
		reply.Question[0].Name = "mixed.example."
		reply.AuthenticatedData = true
		reply.Answer = []dns.RR{&dns.RFC3597{Hdr: dns.RR_Header{Name: "mixed.example.", Rrtype: 65400, Class: dns.ClassINET, Ttl: 60}, Rdata: "c00c010203ff"}}
		reply.SetEdns0(1232, true)
		reply.IsEdns0().Option = []dns.EDNS0{&dns.EDNS0_LOCAL{Code: 65001, Data: []byte{4, 5, 6}}}
		p, err := reply.Pack()
		if err != nil {
			workerErrors <- err
		}
		return testutil.Response{Wire: p}
	})
	require.NoError(t, e)
	defer u.Close()
	c, e := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}})
	require.NoError(t, e)
	r := transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	out := make([]byte, 65535)
	n, e := resolve.New(c).Resolve(context.Background(), &r, out)
	require.NoError(t, e)
	var reply dns.Msg
	require.NoError(t, reply.Unpack(out[:n]))
	assert.Equal(t, q.Id, reply.Id)
	assert.Equal(t, q.Question, reply.Question)
	assert.True(t, reply.CheckingDisabled)
	assert.True(t, reply.RecursionDesired)
	assert.False(t, reply.AuthenticatedData)
	require.Len(t, reply.Answer, 1)
	assert.Equal(t, "c00c010203ff", reply.Answer[0].(*dns.RFC3597).Rdata)
	assert.Equal(t, original, wire, "borrowed input mutated")
	sent := <-u.Requests()
	var forwarded dns.Msg
	require.NoError(t, forwarded.Unpack(sent.Wire))
	assert.True(t, forwarded.CheckingDisabled)
	assert.False(t, forwarded.AuthenticatedData)
	require.NotNil(t, forwarded.IsEdns0())
	assert.True(t, forwarded.IsEdns0().Do())
	assert.EqualValues(t, 1232, forwarded.IsEdns0().UDPSize())
	require.Len(t, forwarded.IsEdns0().Option, 1)
	assert.EqualValues(t, 65001, forwarded.IsEdns0().Option[0].Option())
	select {
	case err := <-workerErrors:
		assert.NoError(t, err)
	default:
	}
}

func TestForwardConcurrentClientOwnership(t *testing.T) {
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		p := append([]byte(nil), r.Wire...)
		p[2] |= 0x80
		return testutil.Response{Wire: p}
	})
	require.NoError(t, e)
	defer u.Close()
	c, e := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}, MaxOutstanding: 32})
	require.NoError(t, e)
	pipeline := resolve.New(c)
	type outcome struct {
		id   byte
		wire []byte
		err  error
	}
	results := make(chan outcome, 24)
	for i := byte(0); i < 24; i++ {
		go func() {
			wire := []byte{0, i, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'A' + i, 0, 0, 1, 0, 1}
			r := transport.Request{Wire: wire}
			if err := dnswire.ParseRequest(wire, &r.Message); err != nil {
				results <- outcome{err: err}
				return
			}
			out := make([]byte, 65535)
			n, err := pipeline.Resolve(context.Background(), &r, out)
			results <- outcome{id: i, wire: out[:n], err: err}
		}()
	}
	for range 24 {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			var m dnswire.Message
			require.NoError(t, dnswire.ScanMessage(result.wire, &m))
			assert.EqualValues(t, result.id, m.Question.Header.ID)
			assert.Equal(t, byte('A')+result.id, m.Question.Name.Wire[1])
		case <-time.After(3 * time.Second):
			require.FailNow(t, "concurrent resolution leaked")
		}
	}
	assert.Zero(t, c.Outstanding())
}
