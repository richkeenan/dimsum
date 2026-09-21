package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedPolicyReloadWithoutListenerRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := []byte(`version: 1
dns:
  listen: [127.0.0.1:0]
  upstreams: [127.0.0.1:9]
admin: {listen: '127.0.0.1:0'}
paths: {data_dir: './data', secrets_dir: './secrets'}
rules:
  - id: block
    action: deny
    kind: exact
    pattern: ads.test
    enabled: true
`)
	require.NoError(t, os.WriteFile(path, text, 0600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := config.OpenStore(ctx, path, filepath.Join(dir, "state"), config.StoreOptions{PollInterval: 5 * time.Millisecond})
	require.NoError(t, err)
	service := new(app.Service)
	require.NoError(t, service.StartManaged(ctx, store))
	defer service.Close()
	address := service.Addresses().DNS[0]
	query := func(name string) (*dns.Msg, error) {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		client := dns.Client{Timeout: time.Second}
		r, _, e := client.Exchange(q, address)
		return r, e
	}
	answer, err := query("ads.test.")
	require.NoError(t, err)
	require.Len(t, answer.Answer, 1)
	assert.Equal(t, "0.0.0.0", answer.Answer[0].(*dns.A).A.String())
	assert.False(t, answer.AuthenticatedData)
	d, err := config.Parse(text)
	require.NoError(t, err)
	d, err = d.Edit([]config.Edit{{Path: []string{"rules", "0", "pattern"}, Value: "next.test"}})
	require.NoError(t, err)
	stage := path + ".replace"
	require.NoError(t, os.WriteFile(stage, d.Bytes(), 0600))
	require.NoError(t, os.Rename(stage, path))
	require.Eventually(t, func() bool { return store.Snapshot().Revision() == d.Revision() }, time.Second, 5*time.Millisecond)
	answer, err = query("next.test.")
	require.NoError(t, err)
	require.Len(t, answer.Answer, 1)
	assert.Equal(t, "0.0.0.0", answer.Answer[0].(*dns.A).A.String())
	assert.Equal(t, address, service.Addresses().DNS[0])
	assert.True(t, service.Ready())
}
