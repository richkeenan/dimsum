package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recompiling or rewriting unchanged inputs breaks the custom-rule fast path.
func TestRuleSaveSharesSubscriptionState(t *testing.T) {
	p, state := fixtureStore(t)
	s, err := OpenStore(context.Background(), p, state, StoreOptions{})
	require.NoError(t, err)
	before := s.Snapshot().subscriptions
	entries, err := os.ReadDir(filepath.Join(state, "subscriptions"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	path := filepath.Join(state, "subscriptions", entries[0].Name())
	info, err := os.Stat(path)
	require.NoError(t, err)
	orphan := filepath.Join(state, "subscriptions", strings.Repeat("f", 64)+".artifact")
	require.NoError(t, os.WriteFile(orphan, nil, 0600))
	_, err = s.Save(context.Background(), s.Snapshot().Revision(), denyDoc(t))
	require.NoError(t, err)
	after := s.Snapshot().subscriptions
	assert.Same(t, before, after)
	assert.Same(t, before.policy, after.policy)
	current, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, current))
	assert.Equal(t, info.ModTime(), current.ModTime())
	assert.FileExists(t, orphan, "ordinary Save must not run subscription directory maintenance")
	manifest, err := os.Stat(filepath.Join(state, "active.manifest"))
	require.NoError(t, err)
	assert.Less(t, manifest.Size(), int64(8192))
}

func manifestFeedStore(t *testing.T, count int) (*Store, *Document) {
	t.Helper()
	var body strings.Builder
	for i := range count {
		fmt.Fprintf(&body, "ads%d.example\n", i)
	}
	feed := body.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(feed)) }))
	t.Cleanup(server.Close)
	p, state := fixtureStore(t)
	d, err := Parse([]byte(storeFixture))
	require.NoError(t, err)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "feed", URL: server.URL, Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, d.Bytes(), 0600))
	s, err := OpenStore(context.Background(), p, state, StoreOptions{Fetcher: lists.NewFetcher(server.Client())})
	require.NoError(t, err)
	return s, d
}

func readManifestTest(t *testing.T, s *Store) recoveryManifest {
	t.Helper()
	b, err := lists.ReadArtifact(filepath.Join(s.state, "active.manifest"), maxManifestBytes)
	require.NoError(t, err)
	var m recoveryManifest
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func writeManifestTest(t *testing.T, s *Store, m recoveryManifest) {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, lists.WriteArtifact(filepath.Join(s.state, "active.manifest"), b))
}

func TestRuleSaveManifestSizeAndOfflineRecovery(t *testing.T) {
	var sizes []int64
	for _, count := range []int{1, 1000, 76000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, d := manifestFeedStore(t, count)
			before := s.Snapshot().subscriptions
			path := filepath.Join(s.state, "subscriptions", before.artifact.Name)
			stamp := time.Unix(1234567890, 0)
			require.NoError(t, os.Chtimes(path, stamp, stamp))
			info, err := os.Stat(path)
			require.NoError(t, err)
			// Delete source caches: edits and restart must use durable membership.
			require.NoError(t, os.RemoveAll(filepath.Join(s.state, "sources")))
			added, err := d.Append([]string{"rules"}, CustomRule{ID: "custom", Action: "allow", Kind: policy.Exact, Pattern: "ads0.example", Enabled: true})
			require.NoError(t, err)
			name, err := policy.NormalizeName("ads0.example")
			require.NoError(t, err)
			for _, candidate := range []*Document{added, d} {
				_, err := s.Save(context.Background(), s.Snapshot().Revision(), candidate)
				require.NoError(t, err)
				assert.Same(t, before, s.Snapshot().subscriptions)
				assert.Same(t, before.policy, s.Snapshot().subscriptions.policy)
				after, err := os.Stat(path)
				require.NoError(t, err)
				assert.True(t, os.SameFile(info, after))
				assert.Equal(t, stamp, after.ModTime())
				want := policy.Block
				if candidate == added {
					want = policy.Allow
				}
				assert.Equal(t, want, s.Snapshot().Policy().Match(name).Result)
				restarted, err := OpenStore(context.Background(), s.path, s.state, StoreOptions{Offline: true})
				require.NoError(t, err)
				decision := restarted.Snapshot().Policy().Match(name)
				assert.Equal(t, want, decision.Result)
				assert.Equal(t, s.Snapshot().Generation(), decision.Generation)
				assert.Equal(t, s.Snapshot().Policy().Match(name).RuleID, decision.RuleID)
			}
			manifest, err := os.Stat(filepath.Join(s.state, "active.manifest"))
			require.NoError(t, err)
			sizes = append(sizes, manifest.Size())
			assert.Less(t, manifest.Size(), int64(8192))
		})
	}
	require.Len(t, sizes, 3)
	assert.Less(t, sizes[2]-sizes[0], int64(64), "only numeric metadata grows with rule count")
}

func TestRecoveryLegacyMigration(t *testing.T) {
	s, d := manifestFeedStore(t, 2)
	var rules []policy.Rule
	for n := uint32(1); ; n++ {
		rule, ok := s.Snapshot().Policy().RuleAt(n)
		if !ok {
			break
		}
		rules = append(rules, rule)
	}
	a := recovery{Generation: 17, Revision: d.Revision(), Config: d.Bytes(), Rules: rules, Sources: s.Inspect().Sources}
	b, err := json.Marshal(a)
	require.NoError(t, err)
	legacy := filepath.Join(s.state, "active.artifact")
	require.NoError(t, lists.WriteArtifact(legacy, b))
	info, err := os.Stat(legacy)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(s.state, "active.manifest")))
	require.NoError(t, os.RemoveAll(filepath.Join(s.state, "subscriptions")))
	require.NoError(t, os.RemoveAll(filepath.Join(s.state, "sources")))
	restarted, err := OpenStore(context.Background(), s.path, s.state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.EqualValues(t, 17, restarted.Snapshot().Generation())
	assert.FileExists(t, filepath.Join(s.state, "active.manifest"))
	after, err := os.Stat(legacy)
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, after))
	assert.Equal(t, info.ModTime(), after.ModTime())
	// A broken new manifest must never silently select the old generation.
	require.NoError(t, os.WriteFile(filepath.Join(s.state, "active.manifest"), []byte("broken"), 0600))
	require.NoError(t, os.WriteFile(s.path, []byte("invalid: ["), 0600))
	_, err = OpenStore(context.Background(), s.path, s.state, StoreOptions{Offline: true})
	require.Error(t, err)
}

func TestManifestRejectsInvalidReferencesAndMembership(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*recoveryManifest)
	}{
		{"version", func(m *recoveryManifest) { m.Version = 2 }},
		{"generation", func(m *recoveryManifest) { m.Generation = 0 }},
		{"revision", func(m *recoveryManifest) { m.Revision = "bad" }},
		{"traversal", func(m *recoveryManifest) { m.Artifact.Name = "../active.artifact" }},
		{"absolute", func(m *recoveryManifest) { m.Artifact.Name = "/tmp/inputs.artifact" }},
		{"uppercase", func(m *recoveryManifest) { m.Artifact.Name = strings.ToUpper(m.Artifact.Name) }},
		{"size", func(m *recoveryManifest) { m.Artifact.Size++ }},
		{"budget", func(m *recoveryManifest) { m.Artifact.Size = maxRecoveryBytes + 1 }},
		{"source", func(m *recoveryManifest) { m.Sources[0].ID = "other" }},
		{"count", func(m *recoveryManifest) { m.Sources[0].Rules++ }},
		{"disabled", func(m *recoveryManifest) { m.Sources[0].Enabled = false }},
		{"unusable", func(m *recoveryManifest) { m.Sources[0].Usable = false }},
		{"missing-status", func(m *recoveryManifest) { m.Sources = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := manifestFeedStore(t, 1)
			m := readManifestTest(t, s)
			test.change(&m)
			writeManifestTest(t, s, m)
			probe := &Store{state: s.state}
			require.Error(t, probe.recover())
			assert.Nil(t, probe.Snapshot())
		})
	}
}

func TestManifestRejectsInvalidSubscriptionInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*subscriptionInputs)
	}{
		{"version", func(a *subscriptionInputs) { a.Version++ }},
		{"url", func(a *subscriptionInputs) { a.Lists[0].URL += "/other" }},
		{"class", func(a *subscriptionInputs) { a.Rules[0].Class = policy.CustomAllow }},
		{"source", func(a *subscriptionInputs) { a.Rules[0].SourceID = "other" }},
		{"id", func(a *subscriptionInputs) { a.Rules[0].ID = "custom:injected" }},
		{"pattern", func(a *subscriptionInputs) { a.Rules[0].Pattern = "bad..name" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := manifestFeedStore(t, 1)
			m := readManifestTest(t, s)
			b, err := lists.ReadArtifact(filepath.Join(s.state, "subscriptions", m.Artifact.Name), maxRecoveryBytes)
			require.NoError(t, err)
			var a subscriptionInputs
			require.NoError(t, json.Unmarshal(b, &a))
			test.change(&a)
			b, err = json.Marshal(a)
			require.NoError(t, err)
			m.Artifact = artifactReference{Name: fmt.Sprintf("%x.artifact", sha256.Sum256(b)), Size: int64(len(b))}
			require.NoError(t, lists.WriteArtifact(filepath.Join(s.state, "subscriptions", m.Artifact.Name), b))
			writeManifestTest(t, s, m)
			require.Error(t, (&Store{state: s.state}).recover())
		})
	}
}

func TestManifestMissingCorruptAndSymlinkInputs(t *testing.T) {
	for _, mode := range []string{"missing", "checksum", "content-address", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := manifestFeedStore(t, 1)
			m := readManifestTest(t, s)
			path := filepath.Join(s.state, "subscriptions", m.Artifact.Name)
			switch mode {
			case "missing":
				require.NoError(t, os.Remove(path))
			case "checksum":
				b, err := os.ReadFile(path)
				require.NoError(t, err)
				b[len(b)-1] ^= 1
				require.NoError(t, os.WriteFile(path, b, 0600))
			case "content-address":
				b, err := lists.ReadArtifact(path, maxRecoveryBytes)
				require.NoError(t, err)
				b = []byte(strings.Replace(string(b), "ads0", "ads1", 1))
				require.NoError(t, lists.WriteArtifact(path, b))
			case "symlink":
				require.NoError(t, os.Rename(path, path+".real"))
				require.NoError(t, os.Symlink(path+".real", path))
			}
			require.Error(t, (&Store{state: s.state}).recover())
			if mode == "missing" || mode == "symlink" {
				before, err := os.ReadFile(s.path)
				require.NoError(t, err)
				candidate, err := s.Snapshot().document.Append([]string{"rules"}, CustomRule{ID: "custom", Action: "deny", Kind: policy.Exact, Pattern: "custom.example", Enabled: true})
				require.NoError(t, err)
				active := s.Snapshot()
				_, err = s.Save(context.Background(), active.Revision(), candidate)
				require.Error(t, err)
				after, err := os.ReadFile(s.path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				assert.Same(t, active, s.Snapshot())
			}
		})
	}
}

func TestRuleSaveQueuedWatcherRechecksUnderBuildLock(t *testing.T) {
	s, d := manifestFeedStore(t, 1)
	added, err := d.Append([]string{"rules"}, CustomRule{ID: "custom", Action: "deny", Kind: policy.Exact, Pattern: "custom.example", Enabled: true})
	require.NoError(t, err)
	s.build.Lock()
	// Model a watcher observing the new bytes while the saving coordinator owns
	// build. Its refresh request must be discarded after activation completes.
	require.NoError(t, Publish(s.path, d.Revision(), added))
	started, done := make(chan struct{}), make(chan error, 1)
	go func() { close(started); _, err := s.reload(context.Background(), true); done <- err }()
	<-started
	_, activationErr := s.activate(context.Background(), added, added.Revision(), true)
	active := s.Snapshot()
	s.build.Unlock()
	require.NoError(t, activationErr)
	require.NoError(t, <-done)
	assert.Same(t, active, s.Snapshot())
	_, err = s.Reload(context.Background())
	require.NoError(t, err)
	assert.Greater(t, s.Snapshot().Generation(), active.Generation(), "explicit refresh still runs")
}

func TestManifestCleanupDoesNotFollowDirectorySymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	name := strings.Repeat("a", 64) + ".artifact"
	path := filepath.Join(outside, name)
	require.NoError(t, os.WriteFile(path, []byte("unrelated"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "subscriptions")))
	s := &Store{state: dir}
	s.cleanupSubscriptions("")
	assert.FileExists(t, path, "maintenance must not follow a directory symlink")
}

func TestManifestCleanupBoundedAndPreservesActive(t *testing.T) {
	s, _ := manifestFeedStore(t, 1)
	dir := filepath.Join(s.state, "subscriptions")
	active := filepath.Join(dir, s.Snapshot().subscriptions.artifact.Name)
	for i := range 1100 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.artifact", i)), nil, 0600))
	}
	unknown := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(unknown, nil, 0600))
	nested := filepath.Join(dir, strings.Repeat("e", 64)+".artifact")
	require.NoError(t, os.Mkdir(nested, 0700))
	s.cleanupSubscriptions(s.Snapshot().subscriptions.artifact.Name)
	assert.FileExists(t, active)
	assert.FileExists(t, unknown)
	assert.DirExists(t, nested)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 79, "one cleanup has bounded work")
}

func TestManifestCancelledAndFailedCandidatesPreserveRecovery(t *testing.T) {
	s, d := manifestFeedStore(t, 1)
	old := s.Snapshot()
	manifest := filepath.Join(s.state, "active.manifest")
	before, err := os.ReadFile(manifest)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Save(ctx, d.Revision(), d)
	require.ErrorIs(t, err, context.Canceled)
	assert.Same(t, old, s.Snapshot())
	after, err := os.ReadFile(manifest)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	// A changed-list candidate writes new immutable inputs, then fails before
	// publishing text because the staging destination is unusable.
	stage := filepath.Join(s.state, "candidate.manifest")
	require.NoError(t, os.Mkdir(stage, 0700))
	changed, err := d.Edit([]Edit{{Path: []string{"lists", "0", "enabled"}, Value: false}})
	require.NoError(t, err)
	_, err = s.Save(context.Background(), d.Revision(), changed)
	require.Error(t, err)
	assert.Same(t, old, s.Snapshot())
	after, err = os.ReadFile(manifest)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	text, err := os.ReadFile(s.path)
	require.NoError(t, err)
	assert.Equal(t, d.Bytes(), text)
	entries, err := os.ReadDir(filepath.Join(s.state, "subscriptions"))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "failed candidate may leave a safe immutable orphan")
	restarted, err := OpenStore(context.Background(), s.path, s.state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.Equal(t, old.Generation(), restarted.Snapshot().Generation())
	entries, err = os.ReadDir(filepath.Join(s.state, "subscriptions"))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "startup reclaims unreferenced candidate inputs")
}

func TestManifestRefreshFallbackUsesActiveSubscriptionOnly(t *testing.T) {
	s, d := manifestFeedStore(t, 2)
	// A subscription ID may overlap the special-rule provenance namespace.
	d, err := d.Edit([]Edit{{Path: []string{"lists", "0", "id"}, Value: "special"}})
	require.NoError(t, err)
	d, err = d.Upsert([]Edit{{Path: []string{"filtering", "mozilla_canary"}, Value: true}})
	require.NoError(t, err)
	_, err = s.Save(context.Background(), s.Snapshot().Revision(), d)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(s.state, "sources")))
	s.options.Offline = true
	_, err = s.Reload(context.Background())
	require.NoError(t, err)
	assert.Contains(t, s.Inspect().Sources[0].Error, "retained active source")
	assert.Equal(t, 2, s.Inspect().Sources[0].Rules)
	name, err := policy.NormalizeName("ads0.example")
	require.NoError(t, err)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(name).Result)
	_, err = OpenStore(context.Background(), s.path, s.state, StoreOptions{Offline: true})
	require.NoError(t, err)
}

func TestRuleSaveReusesUnavailableSubscriptionStatus(t *testing.T) {
	s, d := manifestFeedStore(t, 1)
	changed, err := d.Edit([]Edit{{Path: []string{"lists", "0", "url"}, Value: "http://unavailable.invalid/feed"}})
	require.NoError(t, err)
	s.options.Offline = true
	_, err = s.Save(context.Background(), d.Revision(), changed)
	require.NoError(t, err)
	before := s.Snapshot().subscriptions
	require.False(t, before.sources[0].Usable)
	_, err = s.Save(context.Background(), changed.Revision(), changed)
	require.NoError(t, err)
	assert.Same(t, before, s.Snapshot().subscriptions)
	assert.Equal(t, before.sources, s.Inspect().Sources)
}

func TestRecoveryAggregateOverlayBudgets(t *testing.T) {
	s, d := manifestFeedStore(t, 1)
	d, err := d.Append([]string{"rules"}, CustomRule{ID: "owner-regex", Action: "deny", Kind: policy.Regex, Pattern: "owner", Enabled: true})
	require.NoError(t, err)
	rules := d.value.PolicyRules()
	for i := range policy.DefaultLimits().MaxRegex {
		rules = append(rules, policy.Rule{ID: fmt.Sprintf("4:feed:%d:0", i+1), SourceID: "feed", Class: policy.SubscriptionDeny, Kind: policy.Regex, Pattern: "ads"})
	}
	sources := s.Inspect().Sources
	sources[0].Rules = policy.DefaultLimits().MaxRegex
	a := recovery{Generation: 3, Revision: d.Revision(), Config: d.Bytes(), Rules: rules, Sources: sources}
	b, err := json.Marshal(a)
	require.NoError(t, err)
	require.NoError(t, lists.WriteArtifact(filepath.Join(s.state, "active.artifact"), b))
	require.NoError(t, os.Remove(filepath.Join(s.state, "active.manifest")))
	probe := &Store{state: s.state}
	require.ErrorContains(t, probe.recover(), "aggregate overlay regex budget")
	assert.Nil(t, probe.Snapshot())
	assert.NoFileExists(t, filepath.Join(s.state, "active.manifest"))
}

func TestManifestExistsBeforeOpenReturns(t *testing.T) {
	p, state := fixtureStore(t)
	_, err := OpenStore(context.Background(), p, state, StoreOptions{})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(state, "active.manifest"))
	require.NoError(t, err)
}
