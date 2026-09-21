package config

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourcesOverlapToggleOfflineAndUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ads.test\n")) }))
	defer server.Close()
	p, state := fixtureStore(t)
	ctx := context.Background()
	d := denyDoc(t)
	for _, id := range []string{"a", "b"} {
		var err error
		d, err = d.Append([]string{"lists"}, lists.Subscription{ID: id, URL: server.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
		require.NoError(t, err)
	}
	require.NoError(t, os.WriteFile(p, d.Bytes(), 0600))
	s, err := OpenStore(ctx, p, state, StoreOptions{})
	require.NoError(t, err)
	n, err := policy.NormalizeName("ads.test")
	require.NoError(t, err)
	sources := func() []string {
		return s.Snapshot().Policy().Evaluate(policy.Query{Original: n, Name: n, Explain: true}).SourceIDs
	}
	assert.Equal(t, []string{"a", "b", "custom"}, sources())
	server.Close()
	// Loss of a source cache must not discard rules in the valid active
	// recovery unit during an offline refresh.
	require.NoError(t, os.RemoveAll(filepath.Join(state, "sources")))
	s, err = OpenStore(ctx, p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	_, err = s.Reload(ctx)
	require.NoError(t, err)
	assert.True(t, s.Inspect().SubscriptionAvailable)
	assert.Equal(t, []string{"a", "b", "custom"}, sources())
	d, err = d.Edit([]Edit{{Path: []string{"lists", "0", "enabled"}, Value: false}})
	require.NoError(t, err)
	_, err = s.Save(ctx, s.Inspect().SavedRevision, d)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "custom"}, sources())
	d, err = d.Edit([]Edit{{Path: []string{"lists", "1", "enabled"}, Value: false}})
	require.NoError(t, err)
	_, err = s.Save(ctx, s.Inspect().SavedRevision, d)
	require.NoError(t, err)
	assert.Equal(t, []string{"custom"}, sources())
	assert.False(t, s.Inspect().SubscriptionAvailable)
	// Unusable sources do not prevent valid owner rules from serving.
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "missing", URL: server.URL + "/missing", Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	_, err = s.Save(ctx, s.Inspect().SavedRevision, d)
	require.NoError(t, err)
	assert.False(t, s.Inspect().SubscriptionAvailable)
	assert.NotEmpty(t, s.Inspect().Sources[2].Error)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(n).Result)
}

func TestSupersededBuildPreservesExternalCandidate(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("ads.test\n"))
	}))
	defer server.Close()
	p, state := fixtureStore(t)
	s, err := OpenStore(context.Background(), p, state, StoreOptions{})
	require.NoError(t, err)
	old := s.Snapshot()
	d := denyDoc(t)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "a", URL: server.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	expected := s.Inspect().SavedRevision
	done := make(chan error, 1)
	go func() { _, e := s.Save(context.Background(), expected, d); done <- e }()
	<-entered
	external := []byte("invalid: [external edit")
	writeErr := os.WriteFile(p, external, 0600)
	close(release)
	require.NoError(t, writeErr)
	assert.ErrorIs(t, <-done, ErrConflict)
	assert.Same(t, old, s.Snapshot())
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, external, b)
	restarted, err := OpenStore(context.Background(), p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.Equal(t, old.Generation(), restarted.Snapshot().Generation())
}

func TestConcurrentMatchingAndSaves(t *testing.T) {
	p, state := fixtureStore(t)
	s, err := OpenStore(context.Background(), p, state, StoreOptions{})
	require.NoError(t, err)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				c := snap.Config()
				if len(c.Rules) == 0 {
					continue
				}
				n, e := policy.NormalizeName(c.Rules[0].Pattern)
				if e != nil {
					errs <- e
					return
				}
				d := snap.Policy().Match(n)
				if d.Result != policy.Block || d.Generation != snap.Generation() {
					errs <- fmt.Errorf("mixed snapshot: %+v", d)
					return
				}
			}
		}()
	}
	for i := range 20 {
		d := denyDoc(t)
		d, err = d.Edit([]Edit{{Path: []string{"rules", "0", "pattern"}, Value: fmt.Sprintf("ads%d.test", i)}})
		if err == nil {
			_, err = s.Save(context.Background(), s.Inspect().SavedRevision, d)
		}
		if err != nil {
			break
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	require.NoError(t, err)
	for e := range errs {
		assert.NoError(t, e)
	}
	// Store has a single active pointer, no retained-history slice. Readers own
	// their local references; no promise is made to bound malicious long holders.
	assert.Equal(t, uint64(21), s.Snapshot().Generation())
}
