package lists

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionCandidateBudgetAndCancellation(t *testing.T) {
	for _, transport := range []string{"udp", "doh"} {
		for _, mode := range []string{"same-family", "dual-stack", "cancel"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				var lookups atomic.Int32
				reply := func(wire []byte) []byte {
					var q dns.Msg
					if q.Unpack(wire) != nil {
						return nil
					}
					lookups.Add(1)
					r := new(dns.Msg)
					r.SetReply(&q)
					name := q.Question[0].Name
					if q.Question[0].Qtype == dns.TypeA {
						records := []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4"}
						if mode == "dual-stack" {
							records = records[:3]
						}
						for _, ip := range records {
							r.Answer = append(r.Answer, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(ip)})
						}
					} else if mode == "dual-stack" {
						for _, ip := range []string{"2001:db8::4"} {
							r.Answer = append(r.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: net.ParseIP(ip)})
						}
					}
					b, _ := r.Pack()
					return b
				}
				var options upstream.Options
				if transport == "udp" {
					s, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response { return testutil.Response{Wire: reply(r.Wire)} })
					require.NoError(t, err)
					defer s.Close()
					e, err := upstream.ParseEndpoint(s.Address())
					require.NoError(t, err)
					options.Endpoints = []upstream.Endpoint{e}
				} else {
					s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						b, _ := io.ReadAll(r.Body)
						w.Header().Set("Content-Type", "application/dns-message")
						w.Write(reply(b))
					}))
					defer s.Close()
					options.RootCAs = x509.NewCertPool()
					options.RootCAs.AddCert(s.Certificate())
					e, err := upstream.ParseEndpoint(s.URL + "/dns-query")
					require.NoError(t, err)
					options.Endpoints = []upstream.Endpoint{e}
				}
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "blocked.example\n") }))
				defer destination.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, stopped := make(chan struct{}), make(chan struct{})
				var calls atomic.Int32
				f, err := newFetcherWithDial(options, func(ctx context.Context, network, address string) (net.Conn, error) {
					call := calls.Add(1)
					if mode == "cancel" {
						if call == 1 {
							close(started)
						}
						<-ctx.Done()
						if call == 1 {
							close(stopped)
						}
						return nil, ctx.Err()
					}
					host, _, _ := net.SplitHostPort(address)
					if host == "192.0.2.4" || host == "2001:db8::4" {
						return (&net.Dialer{}).DialContext(ctx, network, destination.Listener.Addr().String())
					}
					<-ctx.Done()
					return nil, ctx.Err()
				})
				require.NoError(t, err)
				tr := f.Client.Transport.(*http.Transport)
				tr.Proxy = nil
				dialFinished := make(chan struct{})
				dial := tr.DialContext
				tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					defer close(dialFinished)
					return dial(ctx, network, address)
				}
				defer f.Client.CloseIdleConnections()
				f.Timeout = time.Second
				if mode == "cancel" {
					go func() { <-started; cancel() }()
				}
				body, _, _, _, err := f.fetch(ctx, Subscription{URL: "http://subscription.example/list"}, nil)
				if mode == "cancel" {
					assert.ErrorIs(t, err, context.Canceled)
					select {
					case <-stopped:
					case <-time.After(time.Second):
						t.Error("abandoned dial did not stop")
					}
					select {
					case <-dialFinished:
					case <-time.After(time.Second):
						t.Error("candidate loop did not stop")
					}
					assert.EqualValues(t, 1, calls.Load())
				} else {
					assert.NoError(t, err)
					assert.Equal(t, "blocked.example\n", string(body))
				}
				assert.EqualValues(t, 2, lookups.Load(), "subscription names use configured DNS for both families")
			})
		}
	}
}
