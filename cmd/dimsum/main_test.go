package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIValidationAndUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	if err := os.WriteFile(path, []byte("version: ["), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"validate", "-config", path}, {"serve", "-config", path}, {"unknown"}, {"validate", "extra"}} {
		var out, stderr bytes.Buffer
		if run(context.Background(), args, &out, &stderr) == 0 {
			t.Fatalf("accepted %v", args)
		}
		if stderr.Len() == 0 {
			t.Fatal("missing diagnostic")
		}
	}
	var out, stderr bytes.Buffer
	if run(context.Background(), []string{"--help"}, &out, &stderr) != 0 || !strings.Contains(out.String(), "validate") {
		t.Fatal("help not discoverable")
	}
	if run(context.Background(), []string{"validate", "-config", "../../testdata/config/dimsum.yaml"}, &out, &stderr) != 0 {
		t.Fatal(stderr.String())
	}
	if !strings.Contains(out.String(), `"active_available":false`) {
		t.Fatal("offline validation pretends to know active state")
	}
}
