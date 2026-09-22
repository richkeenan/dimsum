package dhcp

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func fixtureSettings() Settings {
	return Settings{Enabled: true, Interface: "eth0", ServerIP: "192.0.2.2", Subnet: "192.0.2.0/24", Gateway: "192.0.2.1", RangeStart: "192.0.2.100", RangeEnd: "192.0.2.199", LeaseSeconds: 86400, LocalDomain: "home.arpa", MaxLeases: 1024}
}

func TestDNSConfigurationAcceptsDualStackWildcard(t *testing.T) {
	for _, address := range []string{"192.0.2.2:53", "0.0.0.0:53", "[::]:53"} {
		assert.NoError(t, ValidateDNS(fixtureSettings(), []string{address}), address)
	}
	for _, address := range []string{"[::1]:53", "[2001:db8::1]:53", "[::]:5353", "192.0.2.3:53", "127.0.0.1:53", "invalid"} {
		assert.Error(t, ValidateDNS(fixtureSettings(), []string{address}), address)
	}
}

func TestLocalDomainLeavesRoomForLeaseHostname(t *testing.T) {
	s := fixtureSettings()
	s.LocalDomain = strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 61)
	require.NoError(t, ValidateSettings(s), "189-byte domain plus a 63-byte host label fits the DNS name limit")
	s.LocalDomain += "c"
	assert.Error(t, ValidateSettings(s), "must not configure a domain that can produce oversized lease A/PTR names")
}

func TestSettingsValidation(t *testing.T) {
	require.NoError(t, ValidateSettings(Settings{}))
	require.NoError(t, ValidateSettings(Settings{Interface: "eth0"}))
	require.NoError(t, ValidateSettings(fixtureSettings()))
	cases := []struct {
		name   string
		change func(*Settings)
	}{
		{"missing", func(s *Settings) { s.Interface = "" }},
		{"network", func(s *Settings) { s.ServerIP = "192.0.2.0" }},
		{"broadcast", func(s *Settings) { s.RangeEnd = "192.0.2.255" }},
		{"multicast", func(s *Settings) { s.Gateway = "224.0.0.1" }},
		{"off subnet", func(s *Settings) { s.Gateway = "198.51.100.1" }},
		{"reverse range", func(s *Settings) { s.RangeEnd = "192.0.2.90" }},
		{"wide range", func(s *Settings) {
			s.Subnet = "10.0.0.0/8"
			s.ServerIP = "10.0.0.1"
			s.Gateway = "10.0.0.2"
			s.RangeStart = "10.1.0.0"
			s.RangeEnd = "10.2.0.0"
		}},
		{"overflow", func(s *Settings) { s.Subnet = "0.0.0.0/0"; s.RangeStart = "0.0.0.1"; s.RangeEnd = "255.255.255.254" }},
		{"server in pool", func(s *Settings) { s.RangeStart = s.ServerIP }},
		{"short lease", func(s *Settings) { s.LeaseSeconds = 59 }},
		{"long lease", func(s *Settings) { s.LeaseSeconds = 604801 }},
		{"capacity", func(s *Settings) { s.MaxLeases = 4097 }},
		{"negative capacity", func(s *Settings) { s.MaxLeases = -1 }},
		{"local domain", func(s *Settings) { s.LocalDomain = "test.local" }},
		{"bad domain", func(s *Settings) { s.LocalDomain = "bad_thing.arpa" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { s := fixtureSettings(); tc.change(&s); assert.Error(t, ValidateSettings(s)) })
	}
}

func TestReservationValidation(t *testing.T) {
	good := Reservation{ID: "printer", MAC: "02:00:00:00:00:10", Address: "192.0.2.20", Hostname: "printer"}
	s := fixtureSettings()
	s.Reservations = []Reservation{good}
	require.NoError(t, ValidateSettings(s)) // Outside dynamic pool is supported.
	cases := []struct {
		name   string
		change func(*Settings)
	}{
		{"duplicate ID", func(s *Settings) {
			r := good
			r.Address = "192.0.2.21"
			r.MAC = "02:00:00:00:00:11"
			s.Reservations = append(s.Reservations, r)
		}},
		{"duplicate address", func(s *Settings) {
			r := good
			r.ID = "other"
			r.MAC = "02:00:00:00:00:11"
			s.Reservations = append(s.Reservations, r)
		}},
		{"duplicate identity", func(s *Settings) {
			r := good
			r.ID = "other"
			r.Address = "192.0.2.21"
			s.Reservations = append(s.Reservations, r)
		}},
		{"both identities", func(s *Settings) { s.Reservations[0].ClientID = "0102" }},
		{"no identity", func(s *Settings) { s.Reservations[0].MAC = "" }},
		{"bad hex", func(s *Settings) { s.Reservations[0].MAC = ""; s.Reservations[0].ClientID = "xyz" }},
		{"multicast mac", func(s *Settings) { s.Reservations[0].MAC = "01:00:00:00:00:10" }},
		{"bad hostname", func(s *Settings) { s.Reservations[0].Hostname = "a.b" }},
		{"server", func(s *Settings) { s.Reservations[0].Address = s.ServerIP }},
		{"capacity", func(s *Settings) {
			s.MaxLeases = 1
			r := good
			r.ID = "other"
			r.MAC = "02:00:00:00:00:11"
			r.Address = "192.0.2.21"
			s.Reservations = append(s.Reservations, r)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureSettings()
			s.Reservations = []Reservation{good}
			tc.change(&s)
			assert.Error(t, ValidateSettings(s))
		})
	}
}
