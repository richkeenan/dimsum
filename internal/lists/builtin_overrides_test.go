package lists

import (
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBuiltinCanRemoveEveryException(t *testing.T) {
	sub := Subscription{ID: "work", URL: WorkCompatibilityURL, Dialect: Adblock, DomainKind: policy.Suffix, Enabled: true}
	entries, err := BuiltinEntries(sub)
	require.NoError(t, err)
	sub.BuiltinOverrides = &BuiltinOverrides{}
	for _, entry := range entries {
		sub.BuiltinOverrides.Exclusions = append(sub.BuiltinOverrides.Exclusions, entry.Domain)
	}
	var f *Fetcher
	v, err := f.Refresh(t.Context(), t.TempDir(), sub, true)
	require.NoError(t, err)
	assert.Empty(t, v.Rules)
	assert.Len(t, v.SHA256, 64)
}

func TestBuiltinDomainValidation(t *testing.T) {
	for _, domain := range []string{"192.0.2.1", "192.0.2.1.", "", "*.example", "https://example.test", "@@||example.test^"} {
		_, err := NormalizeBuiltinDomain(domain)
		assert.Error(t, err, domain)
	}
	n, err := NormalizeBuiltinDomain("EXTRA.Example.")
	require.NoError(t, err)
	assert.Equal(t, "extra.example", n)
	for _, o := range []*BuiltinOverrides{
		{Additions: []string{"same.example", "same.example"}},
		{Additions: []string{"same.example"}, Exclusions: []string{"same.example"}},
		{Additions: []string{"UPPER.example"}},
	} {
		assert.Error(t, ValidateBuiltinOverrides(Subscription{URL: WorkCompatibilityURL, BuiltinOverrides: o}))
	}
	assert.Error(t, ValidateBuiltinOverrides(Subscription{URL: "https://example.test/list", BuiltinOverrides: &BuiltinOverrides{}}))
}
