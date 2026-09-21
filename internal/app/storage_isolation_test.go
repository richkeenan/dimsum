package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSContinuesDuringBlockedStorageWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	store, err := config.OpenStore(context.Background(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	s := new(Service)
	require.NoError(t, s.StartManaged(context.Background(), store))
	defer s.Close()
	lock, err := sql.Open("sqlite", filepath.Join(dir, "data", "history.sqlite"))
	require.NoError(t, err)
	defer lock.Close()
	lock.SetMaxOpenConns(1)
	_, err = lock.Exec("BEGIN IMMEDIATE")
	require.NoError(t, err)
	defer lock.Exec("ROLLBACK")
	client := dns.Client{Timeout: 500 * time.Millisecond}
	connection, err := client.Dial(s.Addresses().DNS[0])
	require.NoError(t, err)
	defer connection.Close()
	q := new(dns.Msg)
	q.SetQuestion("1.0.0.10.in-addr.arpa.", dns.TypePTR)
	const total = 1200
	var maximum time.Duration
	for range total {
		answer, elapsed, err := client.ExchangeWithConn(q, connection)
		require.NoError(t, err)
		require.Equal(t, dns.RcodeNameError, answer.Rcode)
		maximum = max(maximum, elapsed)
	}
	require.Eventually(t, func() bool { return s.observability.db.Status().LostDetails > 0 }, 4*time.Second, 10*time.Millisecond)
	counters := s.observability.collector.Snapshot()
	assert.EqualValues(t, total, counters.Admitted)
	assert.EqualValues(t, total, counters.Outcomes[stats.PolicyBlock])
	_, err = s.observability.db.Query(context.Background(), storage.QueryOptions{Start: time.Now().Add(-time.Hour), End: time.Now(), Limit: 1})
	require.NoError(t, err)
	_, err = lock.Exec("ROLLBACK")
	require.NoError(t, err)
	t.Logf("%d real UDP responses while writer blocked; maximum observed client duration %s", total, maximum)
}
