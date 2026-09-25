package clients

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSGuessCatalogueMergesOnlyOwnerChanges(t *testing.T) {
	baseline, err := parseDNSGuessRules(testGuessYAML)
	require.NoError(t, err)
	icon := "bell"
	disabled := true
	changes := DNSGuessOverrides{
		Custom: []DNSGuessRule{{ID: "custom", Name: "Custom device", Domains: []string{"custom.example"}}},
		Rules: map[string]DNSGuessOverride{
			"washer":  {Icon: &icon, Additions: []string{"new.washer.example"}, Exclusions: []string{"firmware.washer.example"}},
			"other":   {Disabled: &disabled},
			"retired": {Exclusions: []string{"old.example"}},
		},
	}
	rules, entries, err := compileDNSGuessCatalogue(baseline, changes)
	require.NoError(t, err)
	exact, _ := rules.selectors()
	assert.ElementsMatch(t, []string{"events.washer.example", "new.washer.example", "custom.example"}, exact)
	require.Len(t, entries, 4)
	for _, entry := range entries {
		switch entry.Rule.ID {
		case "washer":
			assert.Equal(t, "modified", entry.Origin)
			assert.Equal(t, "bell", entry.Rule.Icon)
			require.NotNil(t, entry.Builtin)
			assert.Equal(t, "washing-machine", entry.Builtin.Icon)
		case "retired":
			assert.False(t, entry.Available)
		case "custom":
			assert.Equal(t, "custom", entry.Origin)
			assert.Equal(t, "unknown", entry.Rule.Category)
		}
	}
	// A release can add endpoints without freezing the old built-in definition.
	baseline[0].Domains = append(baseline[0].Domains, "release.washer.example")
	rules, _, err = compileDNSGuessCatalogue(baseline, changes)
	require.NoError(t, err)
	exact, _ = rules.selectors()
	assert.Contains(t, exact, "release.washer.example")
	assert.NotContains(t, exact, "firmware.washer.example")
	assert.Equal(t, "washing-machine", baseline[0].Icon)
	// Reset removes just the overlay; the current release becomes authoritative.
	rules, _, err = compileDNSGuessCatalogue(baseline, DNSGuessOverrides{})
	require.NoError(t, err)
	exact, _ = rules.selectors()
	assert.Contains(t, exact, "firmware.washer.example")
	assert.Contains(t, exact, "release.washer.example")
	assert.NotContains(t, exact, "custom.example")
}

func TestDNSGuessCatalogueRejectsInvalidOwnerChanges(t *testing.T) {
	baseline, err := parseDNSGuessRules(testGuessYAML)
	require.NoError(t, err)
	for _, changes := range []DNSGuessOverrides{
		{Custom: []DNSGuessRule{{ID: "custom", Name: "Custom", Reason: "line one\nline two", Domains: []string{"custom.example"}}}},
		{Custom: []DNSGuessRule{{ID: "custom", Name: "Custom", Domains: []string{"firmware.washer.example"}}}},
		{Custom: []DNSGuessRule{{ID: "custom", Name: "Custom", Domains: []string{"*.example"}}}},
		{Rules: map[string]DNSGuessOverride{"washer": {Additions: []string{"new.example"}, Exclusions: []string{"new.example"}}}},
		{Rules: map[string]DNSGuessOverride{"aws-iot": {Exclusions: []string{"old.example"}}}},
	} {
		_, _, err := compileDNSGuessCatalogue(baseline, changes)
		assert.Error(t, err)
	}
}

func TestDNSGuessViewChangeDiscardsRetiredRefresh(t *testing.T) {
	old, err := NewView(Settings{DNSGuesses: DNSGuessOverrides{Custom: []DNSGuessRule{{ID: "custom", Name: "Custom device", Domains: []string{"custom.example"}, Icon: "bell"}}}}, nil, nil)
	require.NoError(t, err)
	current := old
	m := New(func() *View { return current })
	ip := netip.MustParseAddr("192.0.2.20")
	now := time.Now()
	rows := []DNSActivity{{Address: ip, Domain: "custom.example", First: now.Add(-time.Minute), Last: now, Count: 2}}
	m.ReplaceDNSActivityForView(old, rows)
	assert.Equal(t, "Custom device", m.Get(ip).Name)
	assert.Equal(t, "bell", m.Get(ip).Device.Icon)
	current, err = NewView(Settings{}, nil, nil)
	require.NoError(t, err)
	m.ReplaceDNSActivityForView(old, rows)
	assert.Empty(t, m.Get(ip).Name)
	view, exact, _ := m.DNSGuessSelectors()
	assert.Same(t, current, view)
	assert.NotContains(t, exact, "custom.example")
	assert.True(t, compatibleNames(old, current), "catalogue edits must not invalidate stronger discovery evidence")
}
