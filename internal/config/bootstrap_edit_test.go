package config_test

import (
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestBootstrapSettingsUpsert(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	p := []string{"dns", "bootstrap_dns"}
	next, err := d.Upsert([]config.Edit{{Path: p, Value: []any{"192.0.2.53:53"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.53:53"}, next.Config().DNS.BootstrapDNS)
	next, err = next.Upsert([]config.Edit{{Path: p, Value: []any{"1.1.1.1:53", "9.9.9.9:53"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"1.1.1.1:53", "9.9.9.9:53"}, next.Config().DNS.BootstrapDNS)
	assert.Contains(t, string(next.Bytes()), "# owned by operator\nversion: 1 # schema")
	assert.Contains(t, string(next.Bytes()), "listen: '127.0.0.1:0' # private")
	for _, value := range []any{[]any{}, []any{"https://dns.example/query"}, []any{123}, []any{"0.0.0.0:53"}} {
		_, err = next.Upsert([]config.Edit{{Path: p, Value: value}})
		assert.Error(t, err)
	}
}

func TestBootstrapEditPreservesCommentsAndRefusesAmbiguousEntries(t *testing.T) {
	for _, field := range []string{"  bootstrap_dns: [192.0.2.53:53] # keep\n", "  bootstrap_dns: # keep\n    - 192.0.2.53:53\n"} {
		d, err := config.Parse([]byte(strings.Replace(sample, "dns:\n", "dns:\n"+field, 1)))
		require.NoError(t, err)
		next, err := d.Upsert([]config.Edit{{Path: []string{"dns", "bootstrap_dns"}, Value: []any{"192.0.2.54:53"}}})
		require.NoError(t, err)
		assert.Contains(t, string(next.Bytes()), `bootstrap_dns: ["192.0.2.54:53"] # keep`)
		assert.Contains(t, string(next.Bytes()), "listen: '127.0.0.1:0' # private")
	}
	d, err := config.Parse([]byte(strings.Replace(sample, "dns:\n", "dns:\n  bootstrap_dns:\n    - 192.0.2.53:53 # operator note\n", 1)))
	require.NoError(t, err)
	_, err = d.Upsert([]config.Edit{{Path: []string{"dns", "bootstrap_dns"}, Value: []any{"192.0.2.54:53"}}})
	assert.Error(t, err)
}
