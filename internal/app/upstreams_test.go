package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedUpstreamHotSettingsAndPrivateIsolation(t *testing.T) {
	primary, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, err)
	defer primary.Close()
	fallback, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		p := append([]byte(nil), r.Wire...)
		p[2] |= 0x80
		p[3] = 3
		return testutil.Response{Wire: p}
	})
	require.NoError(t, err)
	defer fallback.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := []byte(fmt.Sprintf(`version: 1
dns:
  listen: [127.0.0.1:0]
  upstreams:
    - %s
  fallback_upstreams: [%s]
  upstream_policy:
    mode: ordered
    timeout_ms: 200
    attempt_timeout_ms: 20
    max_attempts: 1
admin: {listen: '127.0.0.1:0'}
paths: {data_dir: './data', secrets_dir: './secrets'}
`, primary.Address(), fallback.Address()))
	require.NoError(t, os.WriteFile(path, text, 0600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := config.OpenStore(ctx, path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	service := new(app.Service)
	require.NoError(t, service.StartManaged(ctx, store))
	defer service.Close()
	address := service.Addresses().DNS[0]
	exchange := func(name string) *dns.Msg {
		t.Helper()
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		r, _, e := (&dns.Client{Timeout: time.Second}).Exchange(q, address)
		require.NoError(t, e)
		return r
	}
	assert.Equal(t, dns.RcodeServerFailure, exchange("example.com.").Rcode)
	assert.Empty(t, fallback.Requests(), "attempt cap must apply in the actual managed app")
	d, err := config.Parse(text)
	require.NoError(t, err)
	updated, err := d.Edit([]config.Edit{{Path: []string{"dns", "upstream_policy", "max_attempts"}, Value: 2}})
	require.NoError(t, err)
	_, err = store.Save(ctx, d.Revision(), updated)
	require.NoError(t, err)
	assert.Equal(t, dns.RcodeNameError, exchange("example.com.").Rcode)
	assert.Len(t, fallback.Requests(), 1)
	assert.Equal(t, dns.RcodeNameError, exchange("1.0.168.192.in-addr.arpa.").Rcode)
	assert.Len(t, fallback.Requests(), 1, "private reverse must never reach public fallback")
	swapped, err := updated.Edit([]config.Edit{{Path: []string{"dns", "upstreams", "0"}, Value: fallback.Address()}})
	require.NoError(t, err)
	_, err = store.Save(ctx, updated.Revision(), swapped)
	require.NoError(t, err)
	assert.Equal(t, dns.RcodeNameError, exchange("example.com.").Rcode)
	assert.Equal(t, address, service.Addresses().DNS[0])
	assert.False(t, store.Inspect().RestartRequired)
	loop, err := swapped.Edit([]config.Edit{{Path: []string{"dns", "upstreams", "0"}, Value: address}})
	require.NoError(t, err, "port-zero text does not know the bound address")
	_, err = store.Save(ctx, swapped.Revision(), loop)
	assert.ErrorContains(t, err, "endpoint points to DNS listener")
	assert.Equal(t, swapped.Revision(), store.Snapshot().Revision())
}
