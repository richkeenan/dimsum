package resolve_test

import (
	"context"
	"os"
	"path/filepath"
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
}
