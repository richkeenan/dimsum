package config

import (
	"os"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendTrailingCommentsPreserveOwnership(t *testing.T) {
	entry := "rules:\n  - id: first\n    kind: exact\n    action: deny\n    pattern: old.test\n    enabled: true\n"
	value := CustomRule{ID: "second", Kind: policy.Exact, Action: "deny", Pattern: "new.test", Enabled: true}
	for _, comment := range []string{"    # trailing item comment\n", "  # sequence footer\n", "\n    # separated trailing comment\n"} {
		t.Run(strings.TrimSpace(comment), func(t *testing.T) {
			source := []byte(storeFixture + entry + comment + "\n# Client section\nclients: []\n")
			path, _ := fixtureStore(t)
			require.NoError(t, os.WriteFile(path, source, 0600))
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			d, err := Parse(saved)
			require.NoError(t, err)
			candidate, err := d.Append([]string{"rules"}, value)
			require.ErrorContains(t, err, "comment")
			assert.Nil(t, candidate)
			assert.Equal(t, source, d.Bytes(), "rejected edits preserve exact bytes")
			saved, err = os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, source, saved)
		})
	}
	t.Run("next section head comment", func(t *testing.T) {
		suffix := "\n# Client section\nclients: []\n"
		source := []byte(storeFixture + entry + suffix)
		path, _ := fixtureStore(t)
		require.NoError(t, os.WriteFile(path, source, 0600))
		saved, err := os.ReadFile(path)
		require.NoError(t, err)
		d, err := Parse(saved)
		require.NoError(t, err)
		candidate, err := d.Append([]string{"rules"}, value)
		require.NoError(t, err)
		inserted := "  - id: second\n    kind: exact\n    action: deny\n    pattern: new.test\n    enabled: true\n"
		assert.Equal(t, storeFixture+entry+inserted+suffix, string(candidate.Bytes()))
		require.NoError(t, Publish(path, d.Revision(), candidate))
		saved, err = os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, storeFixture+entry+inserted+suffix, string(saved))
		assert.Equal(t, source, d.Bytes(), "original document stays immutable")
		root := candidate.root.Content[0]
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == "clients" {
				assert.Equal(t, "# Client section", root.Content[i].HeadComment)
				return
			}
		}
		require.FailNow(t, "clients section missing")
	})
}

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
