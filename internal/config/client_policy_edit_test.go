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

func TestRemoveDeviceRulePreservesOwnerCommentsAndOtherBytes(t *testing.T) {
	other := "      - id: other\n        name: 'Other' # untouched\n        domains: [other.example]\n"
	source := sample + "naming:\n  dns_guesses:\n    custom:\n      # owner note\n      - id: washer\n        name: Washer # preferred description\n        # endpoint note\n        domains: [washer.example]\n" + other
	d, err := config.Parse([]byte(source))
	require.NoError(t, err)
	d, err = d.RemovePreservingComments([]string{"naming", "dns_guesses", "custom", "0"})
	require.NoError(t, err)
	assert.Contains(t, string(d.Bytes()), other)
	assert.Contains(t, string(d.Bytes()), "# owner note")
	assert.Contains(t, string(d.Bytes()), "# preferred description")
	assert.Contains(t, string(d.Bytes()), "# endpoint note")
	require.Len(t, d.Config().Naming.DNSGuesses.Custom, 1)
	d, err = d.RemovePreservingComments([]string{"naming", "dns_guesses", "custom", "0"})
	require.NoError(t, err)
	assert.Empty(t, d.Config().Naming.DNSGuesses.Custom)
	assert.Contains(t, string(d.Bytes()), "# untouched")
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

func TestPolicyFieldsFlowMapEditsAreBytePreserving(t *testing.T) {
	source := sample + "lists:\n  - id: ads\n    url: https://example.test/list\n    dialect: domains\n    domain_kind: suffix\n    enabled: true\nclients:\n  - id: phone\n    selectors: {addresses: [192.0.2.1]}\n    overrides: {blocking: false, lists: {ads: false}} # keep\n"
	d, err := config.Parse([]byte(source))
	require.NoError(t, err)
	d, err = d.PolicyFields([]config.PolicyField{{Path: []string{"clients", "0", "overrides", "lists", "ads"}, Value: true}})
	require.NoError(t, err)
	assert.Equal(t, strings.Replace(source, "ads: false", "ads: true", 1), string(d.Bytes()))
	d, err = d.PolicyFields([]config.PolicyField{{Path: []string{"clients", "0", "overrides", "blocking"}, Reset: true}})
	require.NoError(t, err)
	assert.Contains(t, string(d.Bytes()), "overrides: {lists: {ads: true}} # keep")
	d, err = d.PolicyFields([]config.PolicyField{{Path: []string{"clients", "0", "overrides", "blocking"}, Value: true}})
	require.NoError(t, err)
	assert.Contains(t, string(d.Bytes()), "lists: {ads: true}, blocking: true}")
}

func TestPolicyFieldsAppendToFlowSequence(t *testing.T) {
	source := sample + "clients: [{address: 192.0.2.1, name: 'Keep'}] # retain\n"
	d, err := config.Parse([]byte(source))
	require.NoError(t, err)
	next, err := d.PolicyFields([]config.PolicyField{{Path: []string{"clients"}, Append: true, Value: config.ClientOverride{Address: "192.0.2.2", Name: "Other"}}})
	require.NoError(t, err)
	assert.Contains(t, string(next.Bytes()), "[{address: 192.0.2.1, name: 'Keep'}, ")
	assert.Contains(t, string(next.Bytes()), "] # retain\n")
	assert.Len(t, next.Config().Clients, 2)
}

func TestPolicyFieldsAppendAtEOFWithoutNewline(t *testing.T) {
	for _, resource := range []string{"clients", "profiles"} {
		for _, ending := range []string{"plain", "commented"} {
			t.Run(resource+"/"+ending, func(t *testing.T) {
				section := "clients:\n  - id: old\n    address: 192.0.2.1"
				var item any = config.ClientOverride{ID: "new", Selectors: config.ClientSelectors{Addresses: []string{"192.0.2.2"}}}
				if resource == "profiles" {
					section = "profiles:\n  - id: old\n    name: Old"
					item = config.Profile{ID: "new"}
				}
				if ending == "commented" {
					section += " # keep"
				}
				source := sample + section
				d, err := config.Parse([]byte(source))
				require.NoError(t, err)
				next, err := d.PolicyFields([]config.PolicyField{{Path: []string{resource}, Append: true, Value: item}})
				require.NoError(t, err)
				assert.True(t, strings.HasPrefix(string(next.Bytes()), source+"\n  - id: new\n"), string(next.Bytes()))
				assert.Equal(t, source, string(d.Bytes()))
				if resource == "clients" {
					clients := next.Config().Clients
					require.Len(t, clients, 2)
					assert.Equal(t, config.ClientOverride{ID: "old", Address: "192.0.2.1"}, clients[0])
					assert.Equal(t, item, clients[1])
				} else {
					assert.Equal(t, []config.Profile{{ID: "old", Name: "Old"}, {ID: "new"}}, next.Config().Profiles)
				}
			})
		}
	}
}
