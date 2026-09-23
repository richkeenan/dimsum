package control

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientPolicyAtomicSubscribePreviewAndConflict(t *testing.T) {
	s, store := dhcpFixture(t)
	_, err := s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "peer", Create: true, Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.11"}}})
	require.NoError(t, err)
	revision := store.Inspect().SavedRevision
	m := ClientPolicyMutation{Revision: revision, Scope: "client", ID: "phone", Create: true, Name: "Phone", Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.10"}}, Subscribe: []PolicySubscription{{ID: "adult", URL: "https://example.test/adult", Dialect: "domains", DomainKind: "exact", Enabled: true}}, Fields: []config.PolicyField{{Path: []string{"lists", "adult"}, Value: true}}}
	before, err := os.ReadFile(s.options.ConfigPath)
	require.NoError(t, err)
	preview, err := s.PreviewClientPolicy(m)
	require.NoError(t, err)
	assert.Equal(t, []string{"phone"}, preview.ChangedClients)
	assert.False(t, preview.NetworkChanged)
	after, err := os.ReadFile(s.options.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	_, err = s.MutateClientPolicy(t.Context(), m)
	require.NoError(t, err)
	c := store.Snapshot().Config()
	require.Len(t, c.Lists, 1)
	require.NotNil(t, c.Lists[0].DefaultApply)
	assert.False(t, *c.Lists[0].DefaultApply)
	phone, ok := store.Snapshot().ClientPolicies().Client("phone")
	require.True(t, ok)
	enabled, _ := phone.List("adult")
	assert.True(t, enabled)
	enabled, _ = store.Snapshot().ClientPolicies().Network().List("adult")
	assert.False(t, enabled)
	_, err = s.MutateClientPolicy(t.Context(), m)
	assert.ErrorIs(t, err, config.ErrConflict)
	m.Revision = store.Inspect().SavedRevision
	m.Create = false
	m.Subscribe = nil
	m.Fields = []config.PolicyField{{Path: []string{"lists", "missing"}, Value: true}}
	saved, err := os.ReadFile(s.options.ConfigPath)
	require.NoError(t, err)
	_, err = s.MutateClientPolicy(t.Context(), m)
	require.Error(t, err)
	after, err = os.ReadFile(s.options.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, saved, after)
	zero := 0
	_, err = s.Mutate(t.Context(), "lists", "DELETE", Mutation{Revision: store.Inspect().SavedRevision, Index: &zero})
	assert.Error(t, err)
}

func TestClientPolicyReenableListPreservesNetworkApplication(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-default", true: "explicit-off"}[explicit], func(t *testing.T) {
			s, store := dhcpFixture(t)
			item := map[string]any{"id": "adult", "url": "https://example.test/adult", "dialect": "domains", "domain_kind": "exact", "enabled": false}
			if explicit {
				item["default_apply"] = false
			}
			_, err := s.Mutate(t.Context(), "lists", "POST", Mutation{Revision: store.Inspect().SavedRevision, Item: item})
			require.NoError(t, err)
			m := ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "profile", ID: "kids", Create: true, Subscribe: []PolicySubscription{{ID: "adult", URL: "https://example.test/adult", Dialect: "domains", DomainKind: "exact", Enabled: true}}, Fields: []config.PolicyField{{Path: []string{"lists", "adult"}, Value: true}}}
			_, err = s.MutateClientPolicy(t.Context(), m)
			require.NoError(t, err)
			c := store.Snapshot().Config()
			require.Len(t, c.Lists, 1)
			assert.True(t, c.Lists[0].Enabled)
			require.NotNil(t, c.Lists[0].DefaultApply)
			assert.False(t, *c.Lists[0].DefaultApply)
			enabled, _ := store.Snapshot().ClientPolicies().Network().List("adult")
			assert.False(t, enabled)
		})
	}
}

func TestClientPolicyLeaseCreateRelinkAndLegacyName(t *testing.T) {
	s, store := dhcpFixture(t)
	s.options.DHCPInspect = func() dhcp.LeaseSnapshot {
		return dhcp.LeaseSnapshot{Generation: store.Snapshot().Generation(), Leases: []dhcp.Lease{{Address: netip.MustParseAddr("192.0.2.10"), MAC: [6]byte{2, 0, 0, 0, 0, 1}, State: dhcp.Bound, Expiry: time.Now().Add(time.Hour)}}}
	}
	_, err := s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Create: true, Name: "Phone", LeaseAddress: "192.0.2.10", Fields: []config.PolicyField{{Path: []string{"blocking"}, Value: false}}})
	require.NoError(t, err)
	c := store.Snapshot().Config().Clients[0]
	assert.Empty(t, c.Address)
	assert.Empty(t, c.Selectors.Addresses)
	assert.Equal(t, []string{"02:00:00:00:00:01"}, c.Selectors.MACs)
	_, err = s.Mutate(t.Context(), "clients", "PATCH", Mutation{Revision: store.Inspect().SavedRevision, Edits: []config.Edit{{Path: []string{"0", "name"}, Value: "New name"}}})
	require.NoError(t, err)
	c = store.Snapshot().Config().Clients[0]
	require.NotNil(t, c.Overrides.Blocking)
	assert.False(t, *c.Overrides.Blocking)
	relink := ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.20"}}}
	preview, err := s.PreviewClientPolicy(relink)
	require.NoError(t, err)
	assert.Equal(t, []string{"phone"}, preview.ChangedClients)
	_, err = s.MutateClientPolicy(t.Context(), relink)
	require.NoError(t, err)
	c = store.Snapshot().Config().Clients[0]
	assert.Equal(t, "phone", c.ID)
	assert.Empty(t, c.Selectors.MACs)
	assert.False(t, *c.Overrides.Blocking)
	s.options.DHCPInspect = func() dhcp.LeaseSnapshot {
		return dhcp.LeaseSnapshot{Generation: store.Snapshot().Generation(), Leases: []dhcp.Lease{{Address: netip.MustParseAddr("192.0.2.10"), MAC: [6]byte{2, 0, 0, 0, 0, 1}, State: dhcp.Bound, Expiry: time.Now().Add(-time.Hour)}}}
	}
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "other", Create: true, LeaseAddress: "192.0.2.10"})
	assert.Error(t, err)
}

func TestClientPolicyExplainAndProfileReferences(t *testing.T) {
	s, store := dhcpFixture(t)
	_, err := s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "profile", ID: "kids", Create: true, Fields: []config.PolicyField{{Path: []string{"rules"}, Value: []config.CustomRule{{ID: "adult", Kind: "exact", Action: "deny", Pattern: "adult.example", Enabled: true}}}}})
	require.NoError(t, err)
	profile := "kids"
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Create: true, Profile: &profile, Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.10"}}})
	require.NoError(t, err)
	result, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: "adult.example", ClientID: "phone"})
	require.NoError(t, err)
	assert.Equal(t, "block", string(result.Decision.Result))
	assert.Equal(t, "kids", result.Decision.Scope.ID)
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "profile", ID: "kids", Delete: true})
	assert.Error(t, err)
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Fields: []config.PolicyField{{Path: []string{"blocking"}, Value: false}}})
	require.NoError(t, err)
	result, err = s.ExplainClientPolicy(ClientPolicyExplain{Name: "adult.example", ClientID: "phone"})
	require.NoError(t, err)
	assert.NotEqual(t, "block", string(result.Decision.Result))
	assert.Empty(t, result.Decision.RuleID)
	result, err = s.ExplainClientPolicy(ClientPolicyExplain{Name: "1.0.168.192.in-addr.arpa", ClientID: "phone", QType: "PTR"})
	require.NoError(t, err)
	assert.Equal(t, "private_reverse", result.Handling)
	assert.Equal(t, "block", string(result.Decision.Result))
	_, err = s.ExplainClientPolicy(ClientPolicyExplain{Name: "adult.example", ClientID: "unknown"})
	assert.Error(t, err)
}

func TestObservedPolicyIdentityUsesOnlyLiveLease(t *testing.T) {
	s, store := dhcpFixture(t)
	_, err := s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Create: true, Selectors: &config.ClientSelectors{MACs: []string{"02:00:00:00:00:01"}}})
	require.NoError(t, err)
	expiry := time.Now().Add(time.Hour)
	s.options.DHCPInspect = func() dhcp.LeaseSnapshot {
		return dhcp.LeaseSnapshot{Generation: store.Snapshot().Generation(), Leases: []dhcp.Lease{{Address: netip.MustParseAddr("192.0.2.10"), MAC: [6]byte{2, 0, 0, 0, 0, 1}, State: dhcp.Bound, Expiry: expiry}}}
	}
	observed := map[string]any{"items": []any{map[string]any{"address": "192.0.2.10", "name": "Phone"}}}
	result, err := s.observedPolicyIdentity(observed)
	require.NoError(t, err)
	row := result["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "phone", row["client_id"])
	assert.Equal(t, "dhcp_mac", row["matching_method"])
	assert.Equal(t, "02:00:00:00:00:01", row["authoritative_mac"])
	expiry = time.Now().Add(-time.Second)
	result, err = s.observedPolicyIdentity(observed)
	require.NoError(t, err)
	row = result["items"].([]any)[0].(map[string]any)
	assert.Empty(t, row["authoritative_mac"])
	assert.Equal(t, "network", row["matching_method"])
}

func TestClientPolicySubscribeDownloadHealthAndRetainedRules(t *testing.T) {
	var fail atomic.Bool
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		_, _ = w.Write([]byte("adult.example\n"))
	}))
	defer feed.Close()
	s, _ := dhcpFixture(t)
	store, err := config.OpenStore(t.Context(), s.options.ConfigPath, filepath.Join(t.TempDir(), "state"), config.StoreOptions{Fetcher: lists.NewFetcher(feed.Client())})
	require.NoError(t, err)
	s.options.Store = store
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Create: true, Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.10"}}, Subscribe: []PolicySubscription{{ID: "adult", URL: feed.URL, Dialect: "domains", DomainKind: "suffix", Enabled: true}}, Fields: []config.PolicyField{{Path: []string{"lists", "adult"}, Value: true}}})
	require.NoError(t, err)
	read, err := s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	require.Len(t, read.Status.Sources, 1)
	assert.True(t, read.Status.Sources[0].Usable)
	explain, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: "adult.example", ClientID: "phone"})
	require.NoError(t, err)
	assert.Equal(t, "block", string(explain.Decision.Result))
	peer, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: "adult.example", Address: "192.0.2.11"})
	require.NoError(t, err)
	assert.Equal(t, "forward", string(peer.Decision.Result))
	fail.Store(true)
	_, err = store.Reload(t.Context())
	require.NoError(t, err)
	read, err = s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	assert.True(t, read.Status.Sources[0].Usable)
	assert.NotEmpty(t, read.Status.Sources[0].Error)
	explain, err = s.ExplainClientPolicy(ClientPolicyExplain{Name: "adult.example", ClientID: "phone"})
	require.NoError(t, err)
	assert.Equal(t, "block", string(explain.Decision.Result))
	// The same failure on a first download saves assignment but cannot claim protection.
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Subscribe: []PolicySubscription{{ID: "unavailable", URL: feed.URL + "/first", Dialect: "domains", DomainKind: "suffix", Enabled: true}}, Fields: []config.PolicyField{{Path: []string{"lists", "unavailable"}, Value: true}}})
	require.NoError(t, err)
	read, err = s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	require.Len(t, read.Status.Sources, 2)
	assert.False(t, read.Status.Sources[1].Usable)
	assert.NotEmpty(t, read.Status.Sources[1].Error)
	assert.True(t, read.Effective.Lists["unavailable"].Value)
}

func TestClientPolicyResetInheritanceAndLocalPrecedence(t *testing.T) {
	s, store := dhcpFixture(t)
	mutate := func(m ClientPolicyMutation) {
		t.Helper()
		m.Revision = store.Inspect().SavedRevision
		_, err := s.MutateClientPolicy(t.Context(), m)
		require.NoError(t, err)
	}
	mutate(ClientPolicyMutation{Scope: "client", ID: "phone", Create: true, Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.10"}}, Fields: []config.PolicyField{{Path: []string{"blocking"}, Value: true}}})
	mutate(ClientPolicyMutation{Scope: "network", Fields: []config.PolicyField{{Path: []string{"blocking"}, Value: false}}})
	read, err := s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	assert.True(t, read.Effective.Blocking.Value)
	mutate(ClientPolicyMutation{Scope: "client", ID: "phone", ResetAll: true})
	read, err = s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	assert.False(t, read.Effective.Blocking.Value)
	mutate(ClientPolicyMutation{Scope: "network", Fields: []config.PolicyField{{Path: []string{"blocking"}, Value: true}}})
	read, err = s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	assert.True(t, read.Effective.Blocking.Value)
	assert.Empty(t, read.Effective.Blocking.Source.Kind)
	_, err = s.Mutate(t.Context(), "records", "POST", Mutation{Revision: store.Inspect().SavedRevision, Item: map[string]any{"name": "local.example", "type": "A", "value": "192.0.2.40", "ttl": 60}})
	require.NoError(t, err)
	_, err = s.Blocking(t.Context(), BlockingMutation{Revision: store.Inspect().SavedRevision, Enabled: false, PauseUntil: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	explain, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: "local.example", ClientID: "phone"})
	require.NoError(t, err)
	assert.Equal(t, "local", explain.Handling)
	assert.Equal(t, "local", string(explain.Decision.Result))
	assert.Empty(t, explain.Decision.RuleID)
	explain, err = s.ExplainClientPolicy(ClientPolicyExplain{Name: "public.example", ClientID: "phone"})
	require.NoError(t, err)
	assert.Equal(t, "paused", string(explain.Decision.Result))
}

func TestClientPolicyUpstreamRoundtripRejectsContradictoryReset(t *testing.T) {
	s, store := dhcpFixture(t)
	_, err := s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "network", Fields: []config.PolicyField{{Path: []string{"upstream"}, Value: map[string]any{"upstreams": []string{"192.0.2.53:53"}, "fallback_upstreams": []string{"192.0.2.54:53"}}}}})
	require.NoError(t, err)
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Create: true, Selectors: &config.ClientSelectors{Addresses: []string{"192.0.2.10"}}, Fields: []config.PolicyField{{Path: []string{"upstream"}, Value: config.UpstreamRoute{Upstreams: []string{"192.0.2.55:53"}}}}})
	require.NoError(t, err)
	read, err := s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.55:53"}, read.Effective.Upstream.Upstreams)
	assert.Empty(t, read.Effective.Upstream.Fallback)
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "client", ID: "phone", Fields: []config.PolicyField{{Path: []string{"upstream"}, Reset: true}}})
	require.NoError(t, err)
	read, err = s.ReadClientPolicy("client", "phone")
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.53:53"}, read.Effective.Upstream.Upstreams)
	assert.Equal(t, []string{"192.0.2.54:53"}, read.Effective.Upstream.Fallback)
	assert.Empty(t, read.Effective.UpstreamSource.Kind)
	before, err := os.ReadFile(s.options.ConfigPath)
	require.NoError(t, err)
	_, err = s.MutateClientPolicy(t.Context(), ClientPolicyMutation{Revision: store.Inspect().SavedRevision, Scope: "network", Fields: []config.PolicyField{{Path: []string{"upstream"}, Reset: true, Value: false}}})
	assert.Error(t, err)
	after, err := os.ReadFile(s.options.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestClientPolicyCreateAtEOFWithoutNewline(t *testing.T) {
	for _, scope := range []string{"client", "profile"} {
		for _, ending := range []string{"plain", "commented"} {
			t.Run(scope+"/"+ending, func(t *testing.T) {
				s, store := dhcpFixture(t)
				base, err := os.ReadFile(s.options.ConfigPath)
				require.NoError(t, err)
				section := "clients:\n  - id: old\n    overrides:\n      blocking: false\n    address: 192.0.2.1"
				m := ClientPolicyMutation{Scope: scope, ID: "new", Create: true}
				if scope == "client" {
					m.Selectors = &config.ClientSelectors{Addresses: []string{"192.0.2.2"}}
				} else {
					section = "profiles:\n  - id: old\n    name: Old"
				}
				if ending == "commented" {
					section += " # keep"
				}
				source := string(base) + section
				require.NoError(t, os.WriteFile(s.options.ConfigPath, []byte(source), 0600))
				_, err = store.Reload(t.Context())
				require.NoError(t, err)
				m.Revision = store.Inspect().SavedRevision
				result, err := s.MutateClientPolicy(t.Context(), m)
				require.NoError(t, err)
				assert.False(t, result.Pending)
				assert.Empty(t, result.Error)
				assert.NotEqual(t, m.Revision, result.SavedRevision)
				assert.Equal(t, result.SavedRevision, result.ActiveRevision)
				saved, err := os.ReadFile(s.options.ConfigPath)
				require.NoError(t, err)
				assert.True(t, strings.HasPrefix(string(saved), source+"\n  - id: new\n"), string(saved))
				d, err := config.Parse(saved)
				require.NoError(t, err)
				assert.Equal(t, d.Revision(), result.SavedRevision)
				assert.Equal(t, d.Revision(), store.Snapshot().Revision())
				for _, c := range []config.Config{d.Config(), store.Snapshot().Config()} {
					if scope == "client" {
						require.Len(t, c.Clients, 2)
						assert.Equal(t, "old", c.Clients[0].ID)
						assert.Equal(t, "192.0.2.1", c.Clients[0].Address)
						assert.Empty(t, c.Clients[0].Selectors)
						require.NotNil(t, c.Clients[0].Overrides.Blocking)
						assert.False(t, *c.Clients[0].Overrides.Blocking)
						assert.Equal(t, config.ClientOverride{ID: "new", Selectors: *m.Selectors}, c.Clients[1])
					} else {
						assert.Equal(t, []config.Profile{{ID: "old", Name: "Old"}, {ID: "new"}}, c.Profiles)
					}
				}
				views := store.Snapshot().ClientPolicies()
				if scope == "client" {
					old := views.Select(netip.MustParseAddr("192.0.2.1"), "")
					created := views.Select(netip.MustParseAddr("192.0.2.2"), "")
					assert.Equal(t, "old", old.ClientID())
					assert.Equal(t, "new", created.ClientID())
					blocking, _ := created.Blocking()
					assert.True(t, blocking, "new identity must not inherit old device's blocking override")
				} else {
					created, ok := views.Profile("new")
					require.True(t, ok)
					assert.Equal(t, "new", created.ProfileID())
				}
			})
		}
	}
}
