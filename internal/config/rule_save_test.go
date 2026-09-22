package config

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuleSaveReusesActiveSubscriptions(t *testing.T) {
	var requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/changed" {
			_, _ = w.Write([]byte("changed.example\n"))
		} else {
			_, _ = w.Write([]byte("feed.example\n"))
		}
	}))
	defer source.Close()
	p, state := fixtureStore(t)
	d, err := Parse([]byte(storeFixture))
	require.NoError(t, err)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "feed", URL: source.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, d.Bytes(), 0600))
	ctx := context.Background()
	s, err := OpenStore(ctx, p, state, StoreOptions{Fetcher: lists.NewFetcher(source.Client())})
	require.NoError(t, err)
	require.EqualValues(t, 1, requests.Load())
	// Recovering the service must also allow edits without accessing the feed.
	s, err = OpenStore(ctx, p, state, StoreOptions{Fetcher: lists.NewFetcher(source.Client())})
	require.NoError(t, err)
	added, err := d.Append([]string{"rules"}, CustomRule{ID: "custom", Action: "deny", Kind: policy.Exact, Pattern: "custom.example", Enabled: true})
	require.NoError(t, err)
	for _, candidate := range []*Document{added, d} {
		result, err := s.Save(ctx, s.Inspect().SavedRevision, candidate)
		require.NoError(t, err)
		assert.Equal(t, result.SavedRevision, result.ActiveRevision)
		assert.False(t, result.Pending)
		assert.EqualValues(t, 1, requests.Load(), "custom rule edits must not refresh subscriptions")
		feed, err := policy.NormalizeName("feed.example")
		require.NoError(t, err)
		assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(feed).Result)
		custom, err := policy.NormalizeName("custom.example")
		require.NoError(t, err)
		want := policy.Forward
		if candidate == added {
			want = policy.Block
		}
		assert.Equal(t, want, s.Snapshot().Policy().Match(custom).Result)
	}
	_, err = s.Reload(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 2, requests.Load(), "explicit refresh must still fetch subscriptions")
	changed, err := d.Edit([]Edit{{Path: []string{"lists", "0", "url"}, Value: source.URL + "/changed"}})
	require.NoError(t, err)
	_, err = s.Save(ctx, s.Inspect().SavedRevision, changed)
	require.NoError(t, err)
	assert.EqualValues(t, 3, requests.Load(), "changed subscriptions must be fetched")
	feed, err := policy.NormalizeName("feed.example")
	require.NoError(t, err)
	updated, err := policy.NormalizeName("changed.example")
	require.NoError(t, err)
	assert.Equal(t, policy.Forward, s.Snapshot().Policy().Match(feed).Result)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(updated).Result)
	disabled, err := changed.Edit([]Edit{{Path: []string{"lists", "0", "enabled"}, Value: false}})
	require.NoError(t, err)
	_, err = s.Save(ctx, s.Inspect().SavedRevision, disabled)
	require.NoError(t, err)
	assert.Equal(t, policy.Forward, s.Snapshot().Policy().Match(updated).Result)
	assert.EqualValues(t, 3, requests.Load())
	_, err = s.Save(ctx, s.Inspect().SavedRevision, changed)
	require.NoError(t, err)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(updated).Result)
	assert.EqualValues(t, 4, requests.Load(), "re-enabled sources must be fetched")
	s, err = OpenStore(ctx, p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(updated).Result)
}

func TestRuleSaveDoesNotReuseSpecialRulesAsSubscriptionMembers(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("feed.example\n"))
	}))
	defer source.Close()
	p, state := fixtureStore(t)
	d, err := Parse([]byte(storeFixture))
	require.NoError(t, err)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "special", URL: source.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	// The special source name is also a valid subscription ID.
	d, err = d.Upsert([]Edit{{Path: []string{"filtering", "mozilla_canary"}, Value: true}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, d.Bytes(), 0600))
	s, err := OpenStore(context.Background(), p, state, StoreOptions{Fetcher: lists.NewFetcher(source.Client())})
	require.NoError(t, err)
	_, err = s.Save(context.Background(), d.Revision(), d)
	require.NoError(t, err)
}

func BenchmarkRuleSaveWithSubscription(b *testing.B) {
	var feed strings.Builder
	for i := range 76000 {
		fmt.Fprintf(&feed, "ads%d.example\n", i)
	}
	body := feed.String()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer source.Close()
	dir := b.TempDir()
	p := filepath.Join(dir, "config.yaml")
	d, err := Parse([]byte(storeFixture))
	require.NoError(b, err)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "feed", URL: source.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(b, err)
	require.NoError(b, os.WriteFile(p, d.Bytes(), 0600))
	s, err := OpenStore(context.Background(), p, filepath.Join(dir, "state"), StoreOptions{Fetcher: lists.NewFetcher(source.Client())})
	require.NoError(b, err)
	added, err := d.Append([]string{"rules"}, CustomRule{ID: "custom", Action: "deny", Kind: policy.Exact, Pattern: "custom.example", Enabled: true})
	require.NoError(b, err)
	candidate, other := added, d
	revision := d.Revision()
	b.ReportAllocs()
	for b.Loop() {
		var result ActivationResult
		result, err = s.Save(context.Background(), revision, candidate)
		if err != nil {
			break
		}
		revision = result.SavedRevision
		candidate, other = other, candidate
	}
	require.NoError(b, err)
}
