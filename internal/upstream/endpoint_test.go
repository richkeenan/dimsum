package upstream

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEndpointIdentity(t *testing.T) {
	for _, s := range []string{"192.0.2.1:53", "[2001:db8::1]:53", "tls://dns.example", "tls://dns.example:8853", "https://dns.example/dns-query", "https://dns.example:8443/a%2Fb?key=x"} {
		e, err := ParseEndpoint(s)
		require.NoError(t, err)
		assert.Equal(t, s, e.String())
		encoded, err := json.Marshal(e)
		require.NoError(t, err)
		var identity string
		require.NoError(t, json.Unmarshal(encoded, &identity))
		assert.Equal(t, s, identity)
	}
	for _, s := range []string{"http://dns.example/dns-query", "dns.example:53", "tls://dns.example/path", "tls://dns.example?x", "https://user:pass@dns.example/dns-query", "https://dns.example/#x", "https://dns.example", "https://dns.example:0/dns-query", "https://dns.example:/dns-query", "tls://0.0.0.0", "tls://[::]", "tls://bad_host", "tls://dns.example#", " https://dns.example/dns-query"} {
		_, err := ParseEndpoint(s)
		assert.Error(t, err, s)
	}
}
