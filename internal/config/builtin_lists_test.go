package config

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinListValidation(t *testing.T) {
	for _, tc := range []struct {
		url     string
		dialect lists.Dialect
		valid   bool
	}{
		{"builtin://work-compatibility", lists.Adblock, true},
		{"builtin://unknown", lists.Adblock, false},
		{"builtin://work-compatibility?extra=1", lists.Adblock, false},
		{"builtin://work-compatibility", lists.Domains, false},
		{"https://example.test/list", lists.Adblock, true},
	} {
		t.Run(tc.url+string(tc.dialect), func(t *testing.T) {
			c := Default()
			c.Lists = []lists.Subscription{{ID: "fixture", URL: tc.url, Dialect: tc.dialect, DomainKind: policy.Suffix, Enabled: true}}
			err := validatePolicy(c)
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestRecoveryUpdatesBuiltinMembershipWithoutDownloadingExternalSources(t *testing.T) {
	path, state := fixtureStore(t)
	d := denyDoc(t)
	var err error
	for _, sub := range []lists.Subscription{
		{ID: "work", URL: "builtin://work-compatibility", Dialect: lists.Adblock, DomainKind: policy.Suffix, Enabled: true},
		{ID: "external", URL: "https://example.test/list", Dialect: lists.Domains, DomainKind: policy.Suffix, Enabled: true},
	} {
		d, err = d.Append([]string{"lists"}, sub)
		require.NoError(t, err)
	}
	require.NoError(t, os.WriteFile(path, d.Bytes(), 0600))
	// Model an older executable's larger list; removal must not be quarantined
	// like an unexpectedly shrinking remote feed. Keep the external feed offline.
	var rules []policy.Rule
	for i := range 300 {
		rules = append(rules, policy.Rule{ID: fmt.Sprintf("4:work:%d:0", i+1), SourceID: "work", Kind: policy.Suffix, Class: policy.SubscriptionAllow, Pattern: fmt.Sprintf("old-%d.example", i)})
	}
	rules = append(rules, policy.Rule{ID: "8:external:1:0", SourceID: "external", Kind: policy.Suffix, Class: policy.SubscriptionDeny, Pattern: "blocked.example"})
	oldHash := strings.Repeat("0", 64)
	statuses := []SourceStatus{
		{ID: "work", Enabled: true, Usable: true, Rules: 300, SHA256: oldHash},
		{ID: "external", Enabled: true, Usable: true, Rules: 1, SHA256: oldHash},
	}
	old := &Store{path: path, state: state}
	subs, err := old.compileSubscriptions(d.Config(), rules, statuses)
	require.NoError(t, err)
	stage, err := old.stageManifest(d, 7, subs)
	require.NoError(t, err)
	require.NoError(t, old.commitManifest(stage))

	s, err := OpenStore(t.Context(), path, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	got := s.Inspect()
	require.Len(t, got.Sources, 2)
	assert.NotEqual(t, oldHash, got.Sources[0].SHA256)
	assert.Positive(t, got.Sources[0].Rules)
	assert.Less(t, got.Sources[0].Rules, 150)
	assert.Empty(t, got.Sources[0].Error)
	assert.Equal(t, statuses[1], got.Sources[1])
	for _, tc := range []struct {
		name   string
		result policy.Result
	}{
		{"old-0.example", policy.Forward},
		{"blocked.example", policy.Block},
	} {
		name, err := policy.NormalizeName(tc.name)
		require.NoError(t, err)
		assert.Equal(t, tc.result, s.Snapshot().Policy().Match(name).Result)
	}
	restarted, err := OpenStore(t.Context(), path, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.Equal(t, got.Sources, restarted.Inspect().Sources)
	assert.Equal(t, s.Snapshot().Generation(), restarted.Snapshot().Generation())
}
