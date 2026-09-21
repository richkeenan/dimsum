package clients

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestMergeDiscoveredPreservesAuthoritativeNames(t *testing.T) {
	now := time.Now()
	for _, source := range []string{"override", "local", "hosts", "router-ptr"} {
		t.Run(source, func(t *testing.T) {
			primary := Name{Name: "Owner TV", Source: source, Fresh: true}
			found := Name{Name: "SONY TV", Source: "dns-sd", Fresh: true, Expires: now.Add(time.Minute), Device: &Enrichment{Category: "tv", Fresh: true, Evidence: []Evidence{{Label: "Sony", Expires: now.Add(time.Minute)}}}}
			got := mergeDiscovered(primary, found, now)
			assert.Equal(t, "Owner TV", got.Name)
			assert.Equal(t, source, got.Source)
			require.NotNil(t, got.Device)
			assert.Equal(t, "tv", got.Device.Category)
			got.Device.Evidence[0].Label = "changed"
			assert.Equal(t, "Sony", found.Device.Evidence[0].Label)
			assert.Equal(t, "Owner TV", mergeDiscovered(primary, found, now.Add(time.Hour)).Name)
		})
	}
	found := Name{Name: "discovered", Source: "mdns", Fresh: true, Expires: now.Add(time.Minute)}
	assert.Equal(t, "discovered", mergeDiscovered(Name{Source: "unknown"}, found, now).Name)
	assert.Empty(t, mergeDiscovered(Name{Source: "unknown"}, found, now.Add(time.Hour)).Name)
}

func TestDeviceClassificationUsesSpecificEvidence(t *testing.T) {
	tests := []struct {
		name     string
		evidence []Evidence
		want     string
	}{
		{"Example-iPhone.local", nil, "phone"}, {"Example-MacBook.local", nil, "laptop"},
		{"ipad.local", nil, "tablet"}, {"imac.local", nil, "desktop"},
		{"linux.local", nil, "unknown"}, {"Android.local", nil, "unknown"},
		{"unknown-7.local", nil, "unknown"}, {"notiphonecase.local", nil, "unknown"},
		{"player.local", []Evidence{{ServiceType: "_airplay._tcp"}}, "unknown"},
		{"printer.local", []Evidence{{ServiceType: "_ipp._tcp"}}, "printer"},
		{"controller.local", []Evidence{{ServiceType: "_wled._tcp"}}, "lighting"},
		{"Android.local", []Evidence{{Model: "KD-EXAMPLE", Manufacturer: "Sony"}}, "tv"},
		{"host.local", []Evidence{{Manufacturer: "Sony"}}, "unknown"},
		{"host.local", []Evidence{{ServiceType: "_home-assistant._tcp"}}, "server"},
		{"host.local", []Evidence{{ServiceType: "_homeconnect._tcp"}}, "appliance"},
		{"host.local", []Evidence{{DeviceType: "camera"}}, "camera"},
		{"host.local", []Evidence{{DeviceType: "speaker"}}, "speaker"},
		{"host.local", []Evidence{{DeviceType: "camera"}, {DeviceType: "printer"}}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.want, func(t *testing.T) {
			got, reason, _ := classifyDevice(tt.name, tt.evidence)
			assert.Equal(t, tt.want, got)
			assert.NotEmpty(t, reason)
		})
	}
}

func TestEnrichmentSelectsUsefulLabelAndExpires(t *testing.T) {
	now := time.Now()
	n := Name{Name: "Android.local", Source: "mdns", Expires: now.Add(time.Minute), Device: &Enrichment{Hostname: "Android.local", Evidence: []Evidence{
		{Source: "dns-sd", Hostname: "Android.local", Label: "Example Television", ServiceType: "_airplay._tcp", Model: "KD-EXAMPLE", Manufacturer: "Sony", Expires: now.Add(time.Minute)},
	}}}
	got := enrichDiscovered(n, now)
	assert.Equal(t, "Example Television", got.Name)
	assert.Equal(t, "tv", got.Device.Category)
	assert.Equal(t, "dns-sd", got.Source)
	assert.Empty(t, enrichDiscovered(n, now.Add(time.Hour)).Name)
	n.Name = "homeassistant.local"
	n.Device.Hostname = n.Name
	n.Device.Evidence[0].Label = "Home"
	assert.Equal(t, "homeassistant.local", enrichDiscovered(n, now).Name)
}
