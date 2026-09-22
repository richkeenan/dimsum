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
		{"Office printer", nil, "printer"}, {"Garden camera", nil, "camera"}, {"Kitchen speaker", nil, "speaker"},
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

func TestOverrideNameCanEnrichAnOtherwiseUnknownCategory(t *testing.T) {
	now := time.Now()
	got := mergeDiscovered(Name{Name: "Office printer", Source: "override", Fresh: true}, Name{Name: "generic.local", Expires: now.Add(time.Minute), Device: &Enrichment{Category: "unknown", Fresh: true, Evidence: []Evidence{}}}, now)
	require.NotNil(t, got.Device)
	assert.Equal(t, "printer", got.Device.Category)
	assert.True(t, got.Device.Inferred)
	assert.Equal(t, "Office printer", got.Name)
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
	n.Device.Evidence[0].Hostname = n.Name
	n.Device.Evidence[0].Label = "Home"
	assert.Equal(t, "homeassistant.local", enrichDiscovered(n, now).Name)
}

func TestNumberedGenericHostnamesPreferVerifiedLabels(t *testing.T) {
	now := time.Now()
	for _, hostname := range []string{"none-2.local", "Linux-12.local.", "android-3.local", "UNKNOWN-7.local", "01234567-89ab-cdef-0123-456789abcdef.local"} {
		n := Name{Name: hostname, Source: "mdns", Expires: now.Add(time.Minute), Device: &Enrichment{Hostname: hostname, Evidence: []Evidence{
			{Source: "dns-sd", Label: "Example TV", ServiceType: "_airplay._tcp", Expires: now.Add(time.Minute)},
		}}}
		assert.Equal(t, "Example TV", enrichDiscovered(n, now).Name, hostname)
	}
	for _, hostname := range []string{"Office-2.local", "Kitchen-speaker-2.local", "Linux-lab.local"} {
		assert.False(t, genericName(hostname), hostname)
	}
}

func TestExpiredDerivedNameYieldsToFreshDiscovery(t *testing.T) {
	now := time.Now()
	for _, source := range []string{"hosts", "router-ptr"} {
		old := Name{Name: "Old cached name", Source: source, Expires: now.Add(-time.Second)}
		live := Name{Name: "Current device", Source: "mdns", Fresh: true, Expires: now.Add(time.Minute)}
		got := mergeDiscovered(old, live, now)
		assert.Equal(t, "Current device", got.Name)
		assert.Equal(t, "mdns", got.Source)
		old.Fresh = true
		assert.Equal(t, "Old cached name", mergeDiscovered(old, live, now).Name)
	}
}

func TestServiceIdentifiersDoNotOutrankUsefulDeviceLabels(t *testing.T) {
	now := time.Now()
	n := Name{Name: "android.local", Source: "mdns", Expires: now.Add(time.Minute), Device: &Enrichment{Hostname: "android.local", Evidence: []Evidence{
		{Source: "dns-sd", Hostname: "android.local", Label: "Display-0123456789abcdef0123456789abcdef", Expires: now.Add(time.Minute)},
		{Source: "dns-sd", Hostname: "android.local", Label: "Example Television", Expires: now.Add(time.Minute)},
	}}}
	assert.Equal(t, "Example Television", enrichDiscovered(n, now).Name)
	for _, label := range []string{"SpotifyConnect", "SpotifyConnect #2", "amazon #0000"} {
		n.Device.Evidence = []Evidence{{Source: "dns-sd", Hostname: "android.local", Label: label, Expires: now.Add(time.Minute)}}
		assert.Equal(t, "android.local", enrichDiscovered(n, now).Name, label)
	}
}
