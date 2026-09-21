package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
)

const sample = "# owned by operator\nversion: 1 # schema\ndns:\n  listen: [\"127.0.0.1:0\"]\nadmin:\n  listen: '127.0.0.1:0' # private\npaths:\n  data_dir:   './data'\n  secrets_dir: './secrets'\n"

func TestConfigParse(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if d.Config().Cache.StaleMode != "immediate" || d.Config().Cache.MaxStaleSeconds != 3600 {
		t.Fatal("approved stale defaults missing")
	}
	for _, bad := range []string{
		strings.Replace(sample, "version: 1", "version: 2", 1),
		sample + "unknown: true\n", sample + "version: 1\n", sample + "---\nversion: 1\n",
		strings.Replace(sample, "'127.0.0.1:0'", "'localhost:53'", 1),
		strings.Replace(sample, "version: 1", "version: null", 1), "dns: [",
		sample + "cache:\n  stale_mode: surprise\n",
	} {
		if _, err := config.Parse([]byte(bad)); err == nil {
			t.Errorf("accepted invalid config: %s", bad)
		}
	}
}

func TestConfigEditPreservesSourceAndGroups(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	next, err := d.Edit([]config.Edit{{Path: []string{"admin", "listen"}, Value: "127.0.0.1:8081"}, {Path: []string{"paths", "data_dir"}, Value: "./new"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(sample, "'127.0.0.1:0'", `"127.0.0.1:8081"`, 1), "'./data'", `"./new"`, 1)
	if string(next.Bytes()) != want {
		t.Fatalf("unrelated bytes changed:\n%s", next.Bytes())
	}
	if string(d.Bytes()) != sample {
		t.Fatal("edit mutated original")
	}
	if _, err := d.Edit([]config.Edit{{Path: []string{"version"}, Value: 99}}); err == nil {
		t.Fatal("invalid edit accepted")
	}
	if _, err := d.Edit([]config.Edit{{Path: []string{"dns", "listen"}, Value: "bad"}}); err == nil {
		t.Fatal("non-scalar edit accepted")
	}
}

func TestConfigPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	d, err := config.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Publish(path, "", d); err != nil {
		t.Fatal(err)
	}
	// An invalid external save must still conflict; never overwrite user intent.
	if err := os.WriteFile(path, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Publish(path, d.Revision(), d); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "invalid: [" {
		t.Fatal("external save overwritten")
	}
	// A staged file abandoned before rename cannot alter the active document.
	if err := os.WriteFile(path+".staged", []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "invalid: [" {
		t.Fatal("stage activated")
	}
}

func TestConfigGroupedPublicationReadersNeverSeeMixedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	a, err := config.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.Edit([]config.Edit{{Path: []string{"admin", "listen"}, Value: "127.0.0.1:8081"}, {Path: []string{"paths", "data_dir"}, Value: "./new"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Publish(path, "", a); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	failures := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			content, err := os.ReadFile(path)
			if err != nil {
				failures <- err
				return
			}
			d, err := config.Parse(content)
			if err != nil {
				failures <- err
				return
			}
			c := d.Config()
			if !((c.Admin.Listen == "127.0.0.1:0" && c.Paths.DataDir == "./data") || (c.Admin.Listen == "127.0.0.1:8081" && c.Paths.DataDir == "./new")) {
				failures <- errors.New("mixed generation")
				return
			}
		}
	})
	defer func() {
		close(stop)
		wg.Wait()
		select {
		case err := <-failures:
			t.Error(err)
		default:
		}
	}()
	current := a
	for i := 0; i < 20; i++ {
		next := b
		if i%2 == 1 {
			next = a
		}
		if err := config.Publish(path, current.Revision(), next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	// Fresh load models restart: the entire last publication is present.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != sample {
		t.Fatal("restart did not read complete last generation")
	}
}

func TestConfigScalarEditBoundaries(t *testing.T) {
	for _, value := range []string{`"./dátá # literal"`, `'./it''s data'`, `./data#literal`, `./data`, `"./quote\"and\\slash"`} {
		source := strings.Replace(sample, "'./data'", value, 1)
		d, err := config.Parse([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		next, err := d.Edit([]config.Edit{{Path: []string{"paths", "data_dir"}, Value: "./changed"}})
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(source, value, `"./changed"`, 1)
		if string(next.Bytes()) != want {
			t.Fatalf("scalar boundary corrupted %q:\n%s", value, next.Bytes())
		}
	}
	d, _ := config.Parse([]byte(sample))
	if _, err := d.Edit([]config.Edit{{Path: []string{"version"}, Value: 1}, {Path: []string{"version"}, Value: 1}}); err == nil {
		t.Fatal("overlapping edit accepted")
	}
	for _, value := range []string{"|\n    ./data", ">\n    ./data", "\"./multi\n    line\"", "./multi\n    line"} {
		source := strings.Replace(sample, "'./data'", value, 1)
		d, err := config.Parse([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Edit([]config.Edit{{Path: []string{"paths", "data_dir"}, Value: "./changed"}}); err == nil {
			t.Fatalf("unsupported multiline edit accepted: %s", value)
		}
	}
}
