package config_test

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardUpstreamConfiguration(t *testing.T) {
	for _, address := range []string{"resolver.example:53", "127.0.0.1:0", "0.0.0.0:53", "224.0.0.1:53", "[::]:53", "[ff02::1]:53", "127.0.0.1"} {
		c := config.Default()
		c.DNS.Upstreams = []string{address}
		assert.Error(t, config.Validate(c), address)
	}
	source := []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:1053, '[::1]:1053'] # ordered\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n")
	d, e := config.Parse(source)
	require.NoError(t, e)
	c := d.Config()
	require.Len(t, c.DNS.Upstreams, 2)
	c.DNS.Upstreams[0] = "changed"
	assert.Equal(t, "127.0.0.1:1053", d.Config().DNS.Upstreams[0])
	assert.Equal(t, source, d.Bytes())
}

func TestRejectSelfListenerUpstreams(t *testing.T) {
	for _, pair := range [][2]string{{"127.0.0.1:5353", "127.0.0.1:5353"}, {"[::1]:5353", "[::1]:5353"}, {"0.0.0.0:5353", "127.0.0.1:5353"}, {"[::]:5353", "[::1]:5353"}, {"[::]:5353", "127.0.0.1:5353"}, {"127.0.0.1:5353", "[::ffff:127.0.0.1]:5353"}} {
		c := config.Default()
		c.DNS.Listen = []string{pair[0]}
		c.DNS.Upstreams = []string{pair[1]}
		assert.ErrorContains(t, config.Validate(c), "listener", pair)
	}
	c := config.Default()
	c.DNS.Listen = []string{"0.0.0.0:5353"}
	c.DNS.Upstreams = []string{"192.0.2.1:5353", "127.0.0.1:5354"}
	assert.NoError(t, config.Validate(c))
}

func TestFallbackValidationAndOwnership(t *testing.T) {
	c := config.Default()
	c.DNS.Upstreams = []string{"192.0.2.1:53"}
	for _, endpoint := range []string{"127.0.0.1:5353", "[::ffff:127.0.0.1]:5353", "https://example.com/dns-query", "tls://192.0.2.1:853", "0.0.0.0:53", "resolver.test:53"} {
		c.DNS.Fallback = []string{endpoint}
		assert.Error(t, config.Validate(c), endpoint)
	}
	c.DNS.Fallback = []string{"192.0.2.2:53"}
	for _, settings := range []config.UpstreamSettings{{Mode: "race"}, {MaxAttempts: 17}, {MaxOutstanding: -1}, {FailureThreshold: -1}, {TimeoutMS: 60001}, {AttemptTimeoutMS: -1}, {OpenMS: 1000, MaxBackoffMS: 500}} {
		c.DNS.UpstreamPolicy = settings
		assert.Error(t, config.Validate(c), settings)
	}
	c.DNS.UpstreamPolicy = config.UpstreamSettings{Mode: "adaptive"}
	require.NoError(t, config.Validate(c))
	source := []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [192.0.2.1:53]\n  fallback_upstreams: [192.0.2.2:53]\nadmin: {listen: '127.0.0.1:0'}\npaths: {data_dir: data, secrets_dir: secrets}\n")
	d, err := config.Parse(source)
	require.NoError(t, err)
	value := d.Config()
	value.DNS.Fallback[0] = "changed"
	assert.Equal(t, "192.0.2.2:53", d.Config().DNS.Fallback[0])
}
