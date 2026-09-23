package control

import (
	"encoding/json"
	"fmt"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLargeClientInventoryCompactSummaries(t *testing.T) {
	base, err := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, err)
	var text strings.Builder
	text.Write(base)
	text.WriteString("profiles:\n  - id: shared\n    policy:\n      blocking: false\n      rules:\n        - {id: common, kind: exact, action: deny, pattern: blocked.example, enabled: true}\nclients:\n")
	for i := 0; i < 4096; i++ {
		fmt.Fprintf(&text, "  - id: device-%d\n    profile: shared\n    selectors: {addresses: [\"2001:db8::%x\"]}\n", i, i+1)
	}
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text.String()), 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(t.TempDir(), "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	service := New(Options{Store: store, ConfigPath: path})
	response, err := service.Clients(t.Context(), nil)
	require.NoError(t, err)
	summaries := response.(map[string]any)["policy_summaries"].(map[string]ClientInventoryPolicy)
	require.Len(t, summaries, 4096)
	for _, id := range []string{"device-0", "device-4095"} {
		require.NotNil(t, summaries[id].Active)
		assert.Equal(t, "shared", summaries[id].Desired.ProfileID)
		assert.False(t, summaries[id].Active.Filtering)
		assert.Zero(t, summaries[id].Desired.OverrideCount)
	}
	encoded, err := json.Marshal(summaries)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "blocked.example")
	assert.NotContains(t, string(encoded), `"rules"`)
	assert.Less(t, len(encoded), 2*1024*1024)
	// A newer saved file must not be presented as the still-active policy.
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(text.String(), "blocking: false", "blocking: true", 1)), 0600))
	response, err = service.Clients(t.Context(), nil)
	require.NoError(t, err)
	summaries = response.(map[string]any)["policy_summaries"].(map[string]ClientInventoryPolicy)
	assert.True(t, summaries["device-4095"].Desired.Filtering)
	assert.False(t, summaries["device-4095"].Active.Filtering)
}
