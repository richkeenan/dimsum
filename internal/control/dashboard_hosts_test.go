package control

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestDashboardHostEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, listen, kind, value, match string
		allowed                          []string
		want                             bool
	}{
		{name: "ipv4", listen: "0.0.0.0:18080", kind: "A", value: "127.0.0.1", want: true},
		{name: "ipv6", listen: "[::1]:18080", kind: "AAAA", value: "::1", want: true},
		{name: "not local", listen: "0.0.0.0:18080", kind: "A", value: "192.0.2.10"},
		{name: "different listener", listen: "192.0.2.10:18080", kind: "A", value: "127.0.0.1"},
		{name: "ipv4 listener ipv6 record", listen: "0.0.0.0:18080", kind: "AAAA", value: "::1"},
		{name: "wildcard", listen: "0.0.0.0:18080", kind: "A", value: "127.0.0.1", match: "wildcard"},
		{name: "alias", listen: "0.0.0.0:18080", kind: "CNAME", value: "localhost"},
		{name: "already accepted", listen: "0.0.0.0:18080", kind: "A", value: "127.0.0.1", allowed: []string{"DASHBOARD.TEST:18080"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Config{Admin: config.Admin{Listen: tc.listen, AllowedHosts: tc.allowed}, Records: []config.Record{{Name: "Dashboard.Test.", Type: tc.kind, Value: tc.value, Match: tc.match}}}
			got := dashboardHosts(c)
			if tc.want {
				assert.Equal(t, []DashboardHost{{Name: "dashboard.test", Host: "dashboard.test:18080", URL: "http://dashboard.test:18080/"}}, got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}

func TestDashboardHostRejectsMixedDestinationsAndProxy(t *testing.T) {
	c := config.Config{Admin: config.Admin{Listen: "0.0.0.0:18080"}, Records: []config.Record{
		{Name: "dashboard.test", Type: "A", Value: "127.0.0.1"},
		{Name: "dashboard.test", Type: "AAAA", Value: "2001:db8::10"},
	}}
	assert.Empty(t, dashboardHosts(c), "a local address must not qualify external addresses for the same name")
	c.Records = c.Records[:1]
	c.Admin.SecureCookies = true
	assert.Empty(t, dashboardHosts(c), "the plaintext listener does not establish a public HTTPS endpoint")
}
