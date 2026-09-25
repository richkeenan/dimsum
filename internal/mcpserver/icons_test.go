package mcpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIconSearchUsesInstalledNamesThroughMCPAndHTTP(t *testing.T) {
	handler := admin.New(control.New(control.Options{}), admin.Options{}).LocalHandler()
	session := connect(t, handler)
	for _, tc := range []struct {
		search string
		want   []string
	}{
		{"plant", []string{"plant-pot"}},
		{" PLANT ", []string{"plant-pot"}},
		{"no-such-icon-example", []string{}},
	} {
		result := call(t, session, "list_icons", map[string]any{"search": tc.search})
		require.False(t, result.IsError, "%+v", result.Content)
		b, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		var got struct {
			Items []string `json:"items"`
		}
		require.NoError(t, json.Unmarshal(b, &got))
		assert.Equal(t, tc.want, got.Items)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/icons", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var all struct {
		Items []string `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &all))
	assert.Contains(t, all.Items, "plant-pot")
	assert.Contains(t, all.Items, "washing-machine")
	assert.True(t, slices.IsSorted(all.Items))
	for i, name := range all.Items {
		assert.NotEmpty(t, name)
		assert.True(t, clients.ValidIcon(name), name)
		if i > 0 {
			assert.NotEqual(t, all.Items[i-1], name)
		}
	}
}
