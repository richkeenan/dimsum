package admin_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestPublishedContractHasResolvableReferences(t *testing.T) {
	b, e := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, e)
	var spec map[string]any
	require.NoError(t, yaml.Unmarshal(b, &spec))
	assert.Equal(t, "3.1.0", spec["openapi"])
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "$ref" {
					ref, ok := child.(string)
					require.True(t, ok)
					require.True(t, strings.HasPrefix(ref, "#/"))
					var target any = spec
					for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
						mapping, ok := target.(map[string]any)
						require.True(t, ok, ref)
						target, ok = mapping[part]
						require.True(t, ok, ref)
					}
					assert.NotNil(t, target, ref)
				} else {
					walk(child)
				}
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(spec)
}
