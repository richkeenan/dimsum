package transport

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A modest burst of upstream waits must not monopolize every UDP worker and
// prevent a locally answerable query from reaching the resolver at all.
func TestUDPDefaultWorkersServeLocalReplyDuringUpstreamWaits(t *testing.T) {
	const waitingQueries = 32
	entered := make(chan struct{}, waitingQueries)
	release := make(chan struct{})
	defer close(release)
	h := HandlerFunc(func(ctx context.Context, r *Request, out []byte) (int, error) {
		if r.Message.Question.Type == dns.TypeTXT {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		return dnswire.BuildReply(out, &r.Message, dnswire.Reply{RecursionAvailable: true}, 1232)
	})
	s, err := New(Options{RequestTimeout: 10 * time.Second}, h)
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ServeUDP(ctx, conn) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Error("UDP workers did not stop")
		}
	})
	client, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	send := func(name string, typ uint16, id uint16) {
		q := new(dns.Msg)
		q.SetQuestion(name, typ)
		q.Id = id
		wire, err := q.Pack()
		require.NoError(t, err)
		_, err = client.Write(wire)
		require.NoError(t, err)
	}
	for i := range waitingQueries {
		send(fmt.Sprintf("slow%d.example.", i), dns.TypeTXT, uint16(i))
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for range waitingQueries {
		select {
		case <-entered:
		case <-deadline.C:
			t.Fatal("upstream waits exhausted the default UDP workers")
		}
	}
	send("local.example.", dns.TypeA, 100)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))
	buf := make([]byte, 512)
	n, err := client.Read(buf)
	require.NoError(t, err, "local reply must finish before upstream waits are released")
	var reply dns.Msg
	require.NoError(t, reply.Unpack(buf[:n]))
	assert.EqualValues(t, 100, reply.Id)
	assert.Equal(t, dns.RcodeSuccess, reply.Rcode)
}
