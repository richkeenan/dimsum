package config

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const storeFixture = `# owner header
version: 1
dns:
  listen: [127.0.0.1:0] # untouched flow
  upstreams: [127.0.0.1:9]
admin:
  listen: '127.0.0.1:0'
paths:
  data_dir: './data'
  secrets_dir: './secrets'
cache:
  stale_mode: off # keep
  max_stale_seconds: 60
`

func TestCollectionEditsPreserveUnrelatedBytes(t *testing.T) {
	d, err := Parse([]byte(storeFixture))
	require.NoError(t, err)
	d, err = d.Append([]string{"lists"}, lists.Subscription{ID: "test", URL: "http://127.0.0.1/list", Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	d, err = d.Append([]string{"rules"}, CustomRule{ID: "custom", Kind: policy.Exact, Action: "deny", Pattern: "ads.test", Enabled: true})
	require.NoError(t, err)
	assert.Contains(t, string(d.Bytes()), storeFixture)
	before := string(d.Bytes())
	d, err = d.Edit([]Edit{{Path: []string{"lists", "0", "enabled"}, Value: false}})
	require.NoError(t, err)
	assert.Equal(t, 1, len(d.Config().Rules))
	assert.False(t, d.Config().Lists[0].Enabled)
	assert.Equal(t, len(before)+1, len(d.Bytes()))
	d, err = d.Remove([]string{"rules", "0"})
	require.NoError(t, err)
	assert.Empty(t, d.Config().Rules)
	assert.Contains(t, string(d.Bytes()), storeFixture)
	d, err = d.Append([]string{"rules"}, CustomRule{ID: "again", Kind: policy.Exact, Action: "deny", Pattern: "other.test", Enabled: true})
	require.NoError(t, err)
	assert.Len(t, d.Config().Rules, 1)
}
