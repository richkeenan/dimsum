package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BenchmarkRuleHTTPWithSubscription measures one authenticated POST + DELETE
// cycle per operation. Set TMPDIR to an isolated directory on the service-data
// filesystem when comparing durable-write performance on another machine.
func BenchmarkRuleHTTPWithSubscription(b *testing.B) {
	const password = "synthetic-rule-benchmark-password"
	hash, err := admin.HashPassword(password)
	require.NoError(b, err)
	for _, count := range []int{1, 1000, 76000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			benchmarkRuleHTTP(b, count, hash, password)
		})
	}
}

func benchmarkRuleHTTP(b *testing.B, count int, hash, password string) {
	var feed strings.Builder
	for i := range count {
		fmt.Fprintf(&feed, "ads%d.example\n", i)
	}
	body := feed.String()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	b.Cleanup(source.Close)
	dir := b.TempDir()
	path, state := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "state")
	doc, err := config.Parse([]byte(`version: 1
dns:
  listen: ["127.0.0.1:0"]
admin:
  listen: "127.0.0.1:0"
paths:
  data_dir: "./data"
  secrets_dir: "./secrets"
cache:
  stale_mode: off
  max_stale_seconds: 60
`))
	require.NoError(b, err)
	doc, err = doc.Append([]string{"lists"}, lists.Subscription{
		ID: "feed", URL: source.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true,
	})
	require.NoError(b, err)
	require.NoError(b, os.WriteFile(path, doc.Bytes(), 0600))
	store, err := config.OpenStore(b.Context(), path, state, config.StoreOptions{Fetcher: lists.NewFetcher(source.Client())})
	require.NoError(b, err)
	watchCtx, cancel := context.WithCancel(b.Context())
	done := make(chan struct{})
	go func() { store.Watch(watchCtx); close(done) }()
	stopWatcher := func() { cancel(); <-done }
	b.Cleanup(stopWatcher)

	service := control.New(control.Options{Store: store, ConfigPath: path})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = admin.New(service, admin.Options{
		PasswordHash: hash, AllowedHosts: []string{server.Listener.Addr().String()},
	}).Handler()
	server.Start()
	b.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = time.Minute
	client.Jar, err = cookiejar.New(nil)
	require.NoError(b, err)
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	err = ruleBenchmarkRequest(b.Context(), client, server.URL, "", "POST", "/session", map[string]string{"password": password}, &login)
	require.NoError(b, err)
	require.NotEmpty(b, login.CSRF)

	custom, err := policy.NormalizeName("custom.example")
	require.NoError(b, err)
	feedName, err := policy.NormalizeName(fmt.Sprintf("ads%d.example", count-1))
	require.NoError(b, err)
	checkPolicy := func(s *config.Store, added bool) error {
		want := policy.Forward
		if added {
			want = policy.Block
		}
		if got := s.Snapshot().Policy().Match(custom).Result; got != want {
			return fmt.Errorf("custom rule: got %v, want %v", got, want)
		}
		if got := s.Snapshot().Policy().Match(feedName).Result; got != policy.Block {
			return fmt.Errorf("subscription rule: got %v, want block", got)
		}
		return nil
	}
	require.NoError(b, checkPolicy(store, false))
	require.Len(b, store.Inspect().Sources, 1)
	require.Equal(b, count, store.Inspect().Sources[0].Rules)
	zero := 0
	add := control.Mutation{Item: map[string]any{"id": "benchmark", "action": "deny", "kind": "exact", "pattern": "custom.example", "enabled": true}}
	remove := control.Mutation{Index: &zero}
	revision := store.Inspect().SavedRevision
	addTimes, deleteTimes := make([]time.Duration, b.N), make([]time.Duration, b.N)
	var result control.Activation
	var loopErr error
	checkActivation := func() error {
		generation, e := strconv.ParseUint(result.ActiveGeneration, 10, 64)
		if e != nil || generation == 0 || result.SavedRevision == "" || result.ActiveRevision != result.SavedRevision || result.Pending || result.Error != "" {
			return fmt.Errorf("mutation not active: %+v", result)
		}
		if store.Snapshot().Generation() != generation {
			return fmt.Errorf("response generation %d differs from published snapshot", generation)
		}
		return nil
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		add.Revision = revision
		started := time.Now()
		loopErr = ruleBenchmarkRequest(b.Context(), client, server.URL, login.CSRF, "POST", "/api/v1/rules", add, &result)
		addTimes[i] = time.Since(started)
		b.StopTimer()
		if loopErr == nil {
			loopErr = checkActivation()
		}
		if loopErr == nil {
			loopErr = checkPolicy(store, true)
		}
		if loopErr != nil {
			break
		}
		remove.Revision = result.SavedRevision
		result = control.Activation{}
		b.StartTimer()
		started = time.Now()
		loopErr = ruleBenchmarkRequest(b.Context(), client, server.URL, login.CSRF, "DELETE", "/api/v1/rules", remove, &result)
		deleteTimes[i] = time.Since(started)
		b.StopTimer()
		if loopErr == nil {
			loopErr = checkActivation()
		}
		if loopErr == nil {
			loopErr = checkPolicy(store, false)
		}
		if loopErr != nil {
			break
		}
		revision = result.SavedRevision
		result = control.Activation{}
		b.StartTimer()
	}
	b.StopTimer()
	require.NoError(b, loopErr)
	reportRuleLatencies(b, "add", addTimes)
	reportRuleLatencies(b, "delete", deleteTimes)

	stopWatcher()
	source.Close()
	// Remove download caches so recovery must use durable active-policy state.
	require.NoError(b, os.RemoveAll(filepath.Join(state, "sources")))
	restarted, err := config.OpenStore(b.Context(), path, state, config.StoreOptions{Offline: true})
	require.NoError(b, err)
	assert.NoError(b, checkPolicy(restarted, false))
	assert.Equal(b, revision, restarted.Inspect().ActiveRevision)
	assert.False(b, restarted.Inspect().Pending)
	assert.Empty(b, restarted.Inspect().Error)
}

func ruleBenchmarkRequest(ctx context.Context, client *http.Client, origin, csrf, method, path string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, data)
	}
	return json.Unmarshal(data, result)
}

func reportRuleLatencies(b *testing.B, operation string, samples []time.Duration) {
	slices.Sort(samples)
	middle := len(samples) / 2
	median := float64(samples[middle])
	if len(samples)%2 == 0 {
		median = (float64(samples[middle-1]) + median) / 2
	}
	b.ReportMetric(float64(samples[0]), operation+"-min-ns/op")
	b.ReportMetric(median, operation+"-median-ns/op")
	b.ReportMetric(float64(samples[len(samples)-1]), operation+"-max-ns/op")
}
