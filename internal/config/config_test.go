package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sample = "# owned by operator\nversion: 1 # schema\ndns:\n  listen: [\"127.0.0.1:0\"]\nadmin:\n  listen: '127.0.0.1:0' # private\npaths:\n  data_dir:   './data'\n  secrets_dir: './secrets'\n"

func TestConfigParse(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	assert.Equal(t, "immediate", d.Config().Cache.StaleMode)
	assert.EqualValues(t, 3600, d.Config().Cache.MaxStaleSeconds)
	for _, bad := range []string{
		strings.Replace(sample, "version: 1", "version: 2", 1),
		sample + "unknown: true\n", sample + "version: 1\n", sample + "---\nversion: 1\n",
		strings.Replace(sample, "'127.0.0.1:0'", "'localhost:53'", 1),
		strings.Replace(sample, "version: 1", "version: null", 1), "dns: [",
		sample + "cache:\n  stale_mode: surprise\n",
	} {
		_, err := config.Parse([]byte(bad))
		assert.Error(t, err, "accepted invalid config: %s", bad)
	}
}

func TestConfigEditPreservesSourceAndGroups(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	next, err := d.Edit([]config.Edit{{Path: []string{"admin", "listen"}, Value: "127.0.0.1:8081"}, {Path: []string{"paths", "data_dir"}, Value: "./new"}})
	require.NoError(t, err)
	want := strings.Replace(strings.Replace(sample, "'127.0.0.1:0'", `"127.0.0.1:8081"`, 1), "'./data'", `"./new"`, 1)
	assert.Equal(t, want, string(next.Bytes()), "unrelated bytes changed")
	assert.Equal(t, sample, string(d.Bytes()), "edit mutated original")
	_, err = d.Edit([]config.Edit{{Path: []string{"version"}, Value: 99}})
	assert.Error(t, err, "invalid edit accepted")
	_, err = d.Edit([]config.Edit{{Path: []string{"dns", "listen"}, Value: "bad"}})
	assert.Error(t, err, "non-scalar edit accepted")
}

func TestConfigPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	require.NoError(t, config.Publish(path, "", d))
	// An invalid external save must still conflict; never overwrite user intent.
	require.NoError(t, os.WriteFile(path, []byte("invalid: ["), 0600))
	assert.ErrorIs(t, config.Publish(path, d.Revision(), d), config.ErrConflict)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "invalid: [", string(got), "external save overwritten")
	// A staged file abandoned before rename cannot alter the active document.
	require.NoError(t, os.WriteFile(path+".staged", []byte(sample), 0600))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "invalid: [", string(got), "stage activated")
}

func TestConfigGroupedPublicationReadersNeverSeeMixedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	a, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	b, err := a.Edit([]config.Edit{{Path: []string{"admin", "listen"}, Value: "127.0.0.1:8081"}, {Path: []string{"paths", "data_dir"}, Value: "./new"}})
	require.NoError(t, err)
	require.NoError(t, config.Publish(path, "", a))
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
			assert.NoError(t, err)
		default:
		}
	}()
	current := a
	for i := 0; i < 20; i++ {
		next := b
		if i%2 == 1 {
			next = a
		}
		require.NoError(t, config.Publish(path, current.Revision(), next))
		current = next
	}
	// Fresh load models restart: the entire last publication is present.
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, sample, string(content), "restart did not read complete last generation")
}

func TestConfigScalarEditBoundaries(t *testing.T) {
	for _, value := range []string{`"./dátá # literal"`, `'./it''s data'`, `./data#literal`, `./data`, `"./quote\"and\\slash"`} {
		source := strings.Replace(sample, "'./data'", value, 1)
		d, err := config.Parse([]byte(source))
		require.NoError(t, err)
		next, err := d.Edit([]config.Edit{{Path: []string{"paths", "data_dir"}, Value: "./changed"}})
		require.NoError(t, err)
		want := strings.Replace(source, value, `"./changed"`, 1)
		assert.Equal(t, want, string(next.Bytes()), "scalar boundary corrupted %q", value)
	}
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	_, err = d.Edit([]config.Edit{{Path: []string{"version"}, Value: 1}, {Path: []string{"version"}, Value: 1}})
	assert.Error(t, err, "overlapping edit accepted")
	for _, value := range []string{"|\n    ./data", ">\n    ./data", "\"./multi\n    line\"", "./multi\n    line"} {
		source := strings.Replace(sample, "'./data'", value, 1)
		d, err := config.Parse([]byte(source))
		require.NoError(t, err)
		_, err = d.Edit([]config.Edit{{Path: []string{"paths", "data_dir"}, Value: "./changed"}})
		assert.Error(t, err, "unsupported multiline edit accepted: %s", value)
	}
}
