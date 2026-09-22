package app

import (
	"context"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

func TestDNSGuessBackfillFromRetainedQueries(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	ip := netip.MustParseAddr("192.0.2.20")
	view, err := clients.NewView(clients.Settings{}, nil, nil)
	require.NoError(t, err)
	names := clients.New(func() *clients.View { return view })
	domain, err := policy.NormalizeName("fw-eventstream.ring.com")
	require.NoError(t, err)
	var events []stats.QueryEvent
	for i := 1; i <= 2; i++ {
		e := stats.QueryEvent{Client: ip.As16(), Sequence: uint64(i), QType: 1, QClass: 1, Outcome: stats.PolicyBlock, Timestamp: now.Add(-time.Duration(i) * time.Minute).UnixMicro()}
		e.QNameLength = uint8(domain.CopyWire(e.QName[:]))
		events = append(events, e)
	}
	require.NoError(t, db.WriteBatch(context.Background(), "boot", events))
	require.NoError(t, refreshDNSGuesses(context.Background(), db, names, now))
	n := names.Get(ip)
	assert.Equal(t, "Ring device", n.Name)
	assert.Equal(t, "dns-guess", n.Source)
	require.NotNil(t, n.Device.DNSGuess)
	assert.Equal(t, uint64(2), n.Device.DNSGuess.Domains[0].Queries)
	// A subsequent empty history window removes derived guesses, not merely labels.
	require.NoError(t, refreshDNSGuesses(context.Background(), db, names, now.Add(25*time.Hour)))
	assert.Empty(t, names.Get(ip).Name)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { runDNSGuesses(ctx, db, names); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}
