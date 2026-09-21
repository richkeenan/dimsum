package config_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/creachadair/tomledit"
	"go.yaml.in/yaml/v3"
)

// This characterization experiment records why neither full-document formatter
// is suitable for edits that must preserve unrelated whitespace byte-for-byte.
func TestConfigFormatExperiment(t *testing.T) {
	y := "# household\nversion: 1  # schema\ndns:\n    listen: '127.0.0.1:5353' # local\n\n# preserve spacing\ndata_dir:   './data'\n"
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(y), &node); err != nil {
		t.Fatal(err)
	}
	yout, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	tom := "# household\nversion=1  # schema\n[dns]\nlisten = '127.0.0.1:5353' # local\n\n# preserve spacing\ndata_dir   = './data'\n"
	doc, err := tomledit.Parse(strings.NewReader(tom))
	if err != nil {
		t.Fatal(err)
	}
	var tout bytes.Buffer
	if err := tomledit.Format(&tout, doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, in, out string }{{"yaml-v3 AST", y, string(yout)}, {"tomledit document", tom, tout.String()}} {
		if !strings.Contains(c.out, "# household") || !strings.Contains(c.out, "# local") {
			t.Fatalf("%s lost comments", c.name)
		}
		if c.in == c.out {
			t.Fatalf("re-evaluate %s: formatter now preserves exact input", c.name)
		}
		t.Logf("%s preserves comments but changes unrelated whitespace:\n%s", c.name, c.out)
	}
}
