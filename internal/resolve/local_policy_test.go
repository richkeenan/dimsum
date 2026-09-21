package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedLocalBeforeRDAndDeny(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: "+dir+"/data\n  secrets_dir: "+dir+"/secrets\nrecords:\n  - {name: host.home.arpa, type: A, value: 192.168.1.2, ttl: 30}\nrules:\n  - {id: deny, kind: exact, action: deny, pattern: host.home.arpa, enabled: true}\n"), 0600))
	store, err := config.OpenStore(context.Background(), path, filepath.Join(dir, "state"), config.StoreOptions{})
	require.NoError(t, err)
	q := new(dns.Msg)
	q.SetQuestion("Host.home.arpa.", dns.TypeA)
	q.RecursionDesired = false
	wire, err := q.Pack()
	require.NoError(t, err)
	r := transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	out := make([]byte, 65535)
	n, err := resolve.NewWithStore(nil, store).Resolve(context.Background(), &r, out)
	require.NoError(t, err)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, dns.RcodeSuccess, got.Rcode)
	require.Len(t, got.Answer, 1)
	assert.Equal(t, "192.168.1.2", got.Answer[0].(*dns.A).A.String())
	assert.False(t, got.AuthenticatedData)
	old := store.Snapshot().Generation()
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(text), "192.168.1.2", "192.168.1.3")), 0600))
	_, err = store.Reload(context.Background())
	require.NoError(t, err)
	assert.Greater(t, store.Snapshot().Generation(), old)
	n, err = resolve.NewWithStore(nil, store).Resolve(context.Background(), &r, out)
	require.NoError(t, err)
	require.NoError(t, got.Unpack(out[:n]))
	require.Len(t, got.Answer, 1)
	assert.Equal(t, "192.168.1.3", got.Answer[0].(*dns.A).A.String())
}

func TestPrivateReverseIsDefaultEvenWithoutStore(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("1.1.168.192.in-addr.arpa.", 12)
	b, e := q.Pack()
	require.NoError(t, e)
	r := transport.Request{Wire: b}
	require.NoError(t, dnswire.ParseRequest(b, &r.Message))
	out := make([]byte, 65535)
	n, e := resolve.New(nil).Resolve(context.Background(), &r, out)
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, 3, got.Rcode)
	assert.Len(t, got.Ns, 1)
}
