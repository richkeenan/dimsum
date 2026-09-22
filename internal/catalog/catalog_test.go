package catalog

import (
	"net/url"
	"os"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyntheticCatalogDialectFixtures(t *testing.T) {
	for _, e := range Entries() {
		t.Run(e.ID, func(t *testing.T) {
			f, err := os.Open("../../testdata/lists/" + e.ID + ".txt")
			require.NoError(t, err)
			defer f.Close()
			r, err := lists.Parse(f, lists.Source{ID: e.ID, Dialect: e.Dialect, DomainKind: policy.Suffix}, lists.DefaultLimits())
			if e.Available {
				require.NoError(t, err)
				assert.NotEmpty(t, r.Rules)
				if e.ID == "hagezi-tif-mini" || e.ID == "oisd-big" {
					assert.Positive(t, r.Rejected)
					assert.NotEmpty(t, r.Diagnostics)
				}
			} else {
				require.Error(t, err)
				assert.Empty(t, r.Rules)
				assert.NotEmpty(t, r.Diagnostics)
			}
		})
	}
}

func TestCatalog(t *testing.T) {
	entries := Entries()
	require.Len(t, entries, 7)
	seen := map[string]bool{}
	enabled := 0
	for _, e := range entries {
		if e.ID == "hagezi-tif-mini" || e.ID == "oisd-big" {
			assert.True(t, e.Available)
			_, err := e.Subscription(true)
			require.NoError(t, err)
		}
		source := e.Source()
		assert.Equal(t, e.ID, source.ID)
		assert.Equal(t, e.Dialect, source.Dialect)
		if e.Dialect == lists.Hosts {
			assert.Equal(t, policy.Exact, source.DomainKind)
		} else {
			assert.Equal(t, policy.Suffix, source.DomainKind)
		}
		assert.False(t, seen[e.ID])
		seen[e.ID] = true
		assert.NotEmpty(t, e.Label)
		assert.NotEmpty(t, e.Description)
		assert.NotEmpty(t, e.Attribution)
		assert.Positive(t, e.UpdateInterval)
		u, err := url.Parse(e.URL)
		require.NoError(t, err)
		assert.Equal(t, "https", u.Scheme)
		assert.NotEmpty(t, u.Host)
		assert.NotEmpty(t, e.Homepage)
		if e.DefaultEnabled {
			enabled++
			assert.True(t, e.Available)
		}
		if !e.Available {
			assert.NotEmpty(t, e.UnavailableReason)
		}
	}
	assert.Equal(t, 1, enabled, "only the hosts compatibility preset is marked as the default choice")
	assert.Equal(t, "stevenblack-unified", entries[0].ID)
	assert.True(t, entries[0].DefaultEnabled)
	assert.True(t, entries[0].Available)
	assert.Empty(t, entries[0].UnavailableReason)
	entries[0].ID = "changed"
	assert.NotEqual(t, "changed", Entries()[0].ID)
}
