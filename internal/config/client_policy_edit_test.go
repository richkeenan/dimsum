package config_test

import (
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyFieldsPreserveCommentsAndReset(t *testing.T) {
	source := sample + "clients:\n  - id: phone\n    name: Phone # label\n    selectors:\n      addresses: [192.0.2.1]\n    overrides:\n      # choice\n      blocking: false # retained\n      lists: {}\n"
	d, err := config.Parse([]byte(source))
	require.NoError(t, err)
	d, err = d.PolicyFields([]config.PolicyField{{Path: []string{"clients", "0", "overrides", "blocking"}, Value: true}})
	require.NoError(t, err)
	assert.Equal(t, strings.Replace(source, "blocking: false", "blocking: true", 1), string(d.Bytes()))
	d, err = d.PolicyFields([]config.PolicyField{{Path: []string{"clients", "0", "overrides", "blocking"}, Reset: true}})
	require.NoError(t, err)
	assert.Nil(t, d.Config().Clients[0].Overrides.Blocking)
	assert.Contains(t, string(d.Bytes()), "# choice\n      # retained\n")
	assert.Contains(t, string(d.Bytes()), "name: Phone # label")
	assert.NotContains(t, string(d.Bytes()), "null")
}

func TestPolicyFieldsAtomicReferencesAndRelink(t *testing.T) {
	d, err := config.Parse([]byte(sample + "clients:\n  - address: 192.0.2.1\n    name: Phone # keep\n"))
	require.NoError(t, err)
	next, err := d.PolicyFields([]config.PolicyField{
		{Path: []string{"clients", "0", "id"}, Value: "phone"},
		{Path: []string{"clients", "0", "overrides", "blocking"}, Value: false},
		{Path: []string{"clients", "0", "address"}, Reset: true},
		{Path: []string{"clients", "0", "selectors"}, Value: config.ClientSelectors{MACs: []string{"02:00:00:00:00:01"}}},
	})
	require.NoError(t, err)
	assert.Empty(t, next.Config().Clients[0].Address)
	assert.Contains(t, string(next.Bytes()), "name: Phone # keep")
	_, err = next.PolicyFields([]config.PolicyField{{Path: []string{"clients", "0", "profile"}, Value: "missing"}})
	assert.Error(t, err)
	assert.Empty(t, next.Config().Clients[0].Profile)
}
