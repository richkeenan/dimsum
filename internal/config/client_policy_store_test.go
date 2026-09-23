package config

import (
	"context"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"net/netip"
	"os"
	"testing"
)

func TestClientPolicyActivationRecoveryAndSubscriptionSharing(t *testing.T) {
	s, d := manifestFeedStore(t, 10)
	old := s.Snapshot()
	subs := old.subscriptions
	c := d.Config()
	c.Lists[0].DefaultApply = boolPtr(false)
	c.Profiles = []Profile{{ID: "kids", Policy: PolicyOverrides{Lists: map[string]bool{"feed": true}}}}
	c.Clients = []ClientOverride{{ID: "tablet", Name: "Tablet", Profile: "kids", Selectors: ClientSelectors{Addresses: []string{"192.0.2.2"}}}}
	b, err := yaml.Marshal(c)
	require.NoError(t, err)
	candidate, err := Parse(b)
	require.NoError(t, err)
	_, err = s.Save(context.Background(), old.Revision(), candidate)
	require.NoError(t, err)
	snap := s.Snapshot()
	require.NotNil(t, snap.ClientPolicies())
	n, _ := policy.NormalizeName("ads0.example")
	assert.Equal(t, policy.Forward, snap.Policy().Match(n).Result)
	assert.Equal(t, policy.Block, snap.ClientPolicies().Select(netip.MustParseAddr("192.0.2.2"), "").Policy().Match(n).Result)
	assert.Same(t, subs.policy, snap.subscriptions.policy, "application-only edits share large indexes")
	assert.Equal(t, policy.Block, old.Policy().Match(n).Result, "held generations remain immutable")
	require.NoError(t, os.WriteFile(s.path, []byte("broken: ["), 0600))
	recovered, err := OpenStore(context.Background(), s.path, s.state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.True(t, recovered.Inspect().Recovered)
	rs := recovered.Snapshot()
	assert.Equal(t, snap.Generation(), rs.Generation())
	assert.Equal(t, policy.Forward, rs.Policy().Match(n).Result)
	selected := rs.ClientPolicies().Select(netip.MustParseAddr("::ffff:192.0.2.2"), "")
	assert.Equal(t, "tablet", selected.ClientID())
	assert.Equal(t, policy.Block, selected.Policy().Match(n).Result)
}
