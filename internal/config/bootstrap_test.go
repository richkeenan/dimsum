package config

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Separate processes isolate both net.DefaultResolver and net/http's cached
// environment proxy settings. All DNS/HTTP traffic stays on isolated loopback.
func TestSubscriptionBootstrap(t *testing.T) {
	mode := os.Getenv("DIMSUM_BOOTSTRAP_TEST")
	if mode == "" {
		for _, mode := range []string{"redirect", "proxy"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestSubscriptionBootstrap$", "-test.v")
				cmd.Env = append(os.Environ(), "DIMSUM_BOOTSTRAP_TEST="+mode, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=", "http_proxy=", "https_proxy=", "all_proxy=", "no_proxy=")
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, string(output))
			})
		}
		return
	}
	var systemCalls, udpCalls, tcpCalls atomic.Int32
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		systemCalls.Add(1)
		return nil, errors.New("system DNS unavailable")
	}}
	t.Cleanup(func() { net.DefaultResolver = original })
	workerErrors := make(chan error, 16)
	upstream, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var q dns.Msg
		if e := q.Unpack(r.Wire); e != nil {
			workerErrors <- e
			return testutil.Response{Drop: true}
		}
		answer := new(dns.Msg)
		answer.SetReply(&q)
		answer.RecursionAvailable = true
		if r.Network == "udp" {
			udpCalls.Add(1)
			answer.Truncated = true
		} else {
			tcpCalls.Add(1)
			if q.Question[0].Qtype == dns.TypeA {
				answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(127, 0, 0, 1)}}
			}
		}
		wire, e := answer.Pack()
		if e != nil {
			workerErrors <- e
		}
		return testutil.Response{Wire: wire}
	})
	require.NoError(t, err)
	defer upstream.Close()
	var base string
	var httpCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls.Add(1)
		if mode == "redirect" && r.URL.Path == "/start" {
			http.Redirect(w, r, strings.Replace(base, "feed.", "target.", 1)+"/list", http.StatusFound)
			return
		}
		if mode == "proxy" && r.URL.Host != "feed.bootstrap.invalid:12345" {
			workerErrors <- fmt.Errorf("proxy received unexpected URL %s", r.URL)
		}
		_, _ = w.Write([]byte("ads.test\n"))
	}))
	defer source.Close()
	base = strings.Replace(source.URL, "127.0.0.1", "feed.bootstrap.invalid", 1)
	sourceURL := base + "/start"
	if mode == "proxy" {
		t.Setenv("HTTP_PROXY", strings.Replace(source.URL, "127.0.0.1", "proxy.bootstrap.invalid", 1))
		sourceURL = "http://feed.bootstrap.invalid:12345/list"
	}
	p, state := fixtureStore(t)
	d, err := Parse([]byte(strings.Replace(storeFixture, "127.0.0.1:9", upstream.Address(), 1)))
	require.NoError(t, err)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "bootstrap", URL: sourceURL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, d.Bytes(), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := OpenStore(ctx, p, state, StoreOptions{})
	require.NoError(t, err)
	assert.True(t, s.Inspect().SubscriptionAvailable, "%+v", s.Inspect())
	n, err := policy.NormalizeName("ads.test")
	require.NoError(t, err)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(n).Result)
	assert.Zero(t, systemCalls.Load(), "bootstrap must not use net.DefaultResolver")
	assert.Positive(t, udpCalls.Load())
	assert.Positive(t, tcpCalls.Load(), "truncated UDP must fall back to configured DNS over TCP")
	if mode == "redirect" {
		assert.EqualValues(t, 2, httpCalls.Load())
	} else {
		assert.EqualValues(t, 1, httpCalls.Load())
	}
	require.NoError(t, upstream.Close())
	close(workerErrors)
	for e := range workerErrors {
		assert.NoError(t, e)
	}
}
