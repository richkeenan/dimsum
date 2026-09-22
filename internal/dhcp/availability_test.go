package dhcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentAvailability(t *testing.T) {
	for _, tc := range []struct {
		os, deployment, code string
		supported            bool
	}{
		{"linux", "", "supported", true},
		{"linux", "docker-desktop", "docker_desktop", false},
		{"darwin", "", "unsupported_platform", false},
		{"windows", "", "unsupported_platform", false},
		{"linux", "misspelled", "unknown_deployment", false},
	} {
		t.Run(tc.os+"/"+tc.deployment, func(t *testing.T) {
			got := deploymentAvailability(tc.os, tc.deployment)
			assert.Equal(t, tc.supported, got.Supported)
			assert.Equal(t, tc.code, got.Code)
			if !tc.supported {
				assert.NotEmpty(t, got.Reason)
			}
		})
	}
}

func TestDesktopRejectsPacketsBeforeOpeningInterface(t *testing.T) {
	t.Setenv("DIMSUM_DEPLOYMENT", "docker-desktop")
	_, _, err := OpenSystemLink(Settings{Enabled: true, Interface: "nonexistent-test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
	_, err = ProbeServers(t.Context(), Settings{Interface: "nonexistent-test"}, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
}
