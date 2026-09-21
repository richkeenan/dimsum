package config_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/creachadair/tomledit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// This characterization experiment records why neither full-document formatter
// is suitable for edits that must preserve unrelated whitespace byte-for-byte.
func TestConfigFormatExperiment(t *testing.T) {
	y := "# household\nversion: 1  # schema\ndns:\n    listen: '127.0.0.1:5353' # local\n\n# preserve spacing\ndata_dir:   './data'\n"
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(y), &node))
	yout, err := yaml.Marshal(&node)
	require.NoError(t, err)
	tom := "# household\nversion=1  # schema\n[dns]\nlisten = '127.0.0.1:5353' # local\n\n# preserve spacing\ndata_dir   = './data'\n"
	doc, err := tomledit.Parse(strings.NewReader(tom))
	require.NoError(t, err)
	var tout bytes.Buffer
	require.NoError(t, tomledit.Format(&tout, doc))
	for _, c := range []struct{ name, in, out string }{{"yaml-v3 AST", y, string(yout)}, {"tomledit document", tom, tout.String()}} {
		assert.Contains(t, c.out, "# household", "%s lost comments", c.name)
		assert.Contains(t, c.out, "# local", "%s lost comments", c.name)
		assert.NotEqual(t, c.in, c.out, "re-evaluate %s: formatter now preserves exact input", c.name)
		t.Logf("%s preserves comments but changes unrelated whitespace:\n%s", c.name, c.out)
	}
}
