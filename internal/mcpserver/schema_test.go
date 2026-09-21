package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/richkeenan/dimsum/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompilerRejectsInvalidOptIns(t *testing.T) {
	for _, tc := range []struct{ name, path, schema string }{
		{"token", "/api/v1/tokens", `{"type":"string"}`},
		{"password", "/api/v1/password", `{"type":"string"}`},
		{"session", "/session", `{"type":"string"}`},
		{"stream", "/api/v1/events", `{"type":"string"}`},
		{"archive", "/api/v1/config/backups/{id}", `{"type":"string"}`},
		{"external_ref", "/api/v1/example", `{"$ref":"https://example.com/schema"}`},
		{"missing_ref", "/api/v1/example", `{"$ref":"#/components/schemas/Missing"}`},
		{"cyclic_ref", "/api/v1/example", `{"$ref":"#/components/schemas/Cycle"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := `{"paths":{"` + tc.path + `":{"x-mcp-tools":{"get":{"name":"test","description":"test"}},"get":{"parameters":[{"name":"value","in":"query","schema":` + tc.schema + `}]}}},"components":{"schemas":{"Cycle":{"$ref":"#/components/schemas/Cycle"}}}}`
			_, err := compile([]byte(document))
			require.Error(t, err)
		})
	}
}

func TestToolSchemasDeclareObjectRoots(t *testing.T) {
	document, err := api.JSON()
	require.NoError(t, err)
	ops, err := compile(document)
	require.NoError(t, err)
	for _, op := range ops {
		for name, value := range map[string]any{"inputSchema": op.tool.InputSchema, "outputSchema": op.tool.OutputSchema} {
			if value == nil {
				continue
			}
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			var schema map[string]any
			require.NoError(t, json.Unmarshal(encoded, &schema))
			assert.Equal(t, "object", schema["type"], "%s.%s must satisfy the MCP wire contract", op.tool.Name, name)
		}
	}
}

func TestResponseCaptureBoundsAcrossWrites(t *testing.T) {
	w := &boundedResponse{header: make(http.Header)}
	chunk := make([]byte, 1024)
	for range MaxResponseBytes / len(chunk) {
		n, err := w.Write(chunk)
		require.NoError(t, err)
		assert.Equal(t, len(chunk), n)
	}
	n, err := w.Write([]byte("x"))
	require.Error(t, err)
	assert.Zero(t, n)
	assert.True(t, w.overflow)
	assert.Equal(t, MaxResponseBytes, w.body.Len())
	_, err = w.Write([]byte("again"))
	assert.Error(t, err)
	assert.Equal(t, MaxResponseBytes, w.body.Len())
}

func TestEmbeddedSpecAndCancelledCall(t *testing.T) {
	document, err := api.JSON()
	require.NoError(t, err)
	assert.True(t, json.Valid(document))
	ops, err := compile(document)
	require.NoError(t, err)
	ctx := t.Context()
	var selected operation
	for _, op := range ops {
		if op.tool.Name == "get_summary" {
			selected = op
		}
	}
	require.NotNil(t, selected.tool)
	// A normal call inherits context values/deadlines from the MCP request.
	result := selected.call(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, ctx, r.Context())
		_, _ = io.WriteString(w, `{}`)
	}), nil)
	assert.False(t, result.IsError)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result = selected.call(cancelled, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("cancelled request invoked API")
	}), nil)
	assert.True(t, result.IsError)
}
