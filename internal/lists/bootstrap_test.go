package lists

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetcherWithoutBootstrapFailsClosed(t *testing.T) {
	var systemCalls atomic.Int32
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		systemCalls.Add(1)
		return nil, errors.New("unexpected system DNS")
	}}
	t.Cleanup(func() { net.DefaultResolver = original })
	f := NewFetcher(nil)
	// Keep this no-upstream test independent of machine proxy environment.
	f.Client.Transport.(*http.Transport).Proxy = nil
	f.Timeout = time.Second
	_, err := f.Refresh(context.Background(), t.TempDir(), Subscription{ID: "closed", URL: "http://no-bootstrap.invalid/list", Dialect: Domains, DomainKind: policy.Exact, Enabled: true}, false)
	require.ErrorContains(t, err, "no explicit bootstrap DNS upstreams")
	assert.Zero(t, systemCalls.Load())
}

func TestBootstrapEndpointsAreLiteral(t *testing.T) {
	for _, address := range []string{"recursive.invalid:53", "127.0.0.1:0", "0.0.0.0:53", "224.0.0.1:53"} {
		_, err := NewFetcherWithUpstreams([]string{address})
		assert.Error(t, err, address)
	}
}
