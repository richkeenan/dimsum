package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/app"
)

func TestLifecycleMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	if err := os.WriteFile(path, []byte("version: ["), 0600); err != nil {
		t.Fatal(err)
	}
	s := new(app.Service)
	if err := s.StartFile(context.Background(), path); err == nil {
		s.Close()
		t.Fatal("malformed file started service")
	}
	if a := s.Addresses(); len(a.DNS) != 0 || a.Admin != "" {
		t.Fatal("partial start")
	}
}
