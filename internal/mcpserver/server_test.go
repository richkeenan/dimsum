package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/richkeenan/dimsum/internal/mcpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func connect(t *testing.T, api http.Handler) *mcp.ClientSession {
	t.Helper()
	h, err := mcpserver.New(api)
	require.NoError(t, err)
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return result
}

func TestToolsDerivedFromSpec(t *testing.T) {
	session := connect(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("listing invoked API") }))
	list, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	tools := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"get_summary", "list_queries", "get_query", "get_rankings", "get_timeseries", "list_clients", "get_settings", "list_filter_lists", "get_catalog", "explain_domain", "update_settings", "create_job", "commit_configuration"} {
		require.Contains(t, tools, name)
		assert.NotEmpty(t, tools[name].Description)
	}
	for name := range tools {
		assert.NotContains(t, name, "token")
		assert.NotContains(t, name, "session")
		assert.NotContains(t, name, "password")
		assert.NotContains(t, name, "download")
		assert.NotContains(t, name, "event")
	}
	assert.True(t, tools["get_summary"].Annotations.ReadOnlyHint)
	assert.True(t, tools["explain_domain"].Annotations.ReadOnlyHint)
	assert.False(t, tools["update_settings"].Annotations.ReadOnlyHint)
	require.NotNil(t, tools["update_settings"].Annotations.DestructiveHint)
	assert.True(t, *tools["update_settings"].Annotations.DestructiveHint)
	schema, err := json.Marshal(tools["list_queries"].InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(schema), `"default":100`)
	assert.Contains(t, string(schema), `"blocked"`)
	assert.NotContains(t, string(schema), `"$ref"`)
	output, err := json.Marshal(tools["get_query"].OutputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(output), `"Unsigned integer encoded as a decimal string`)
	assert.NotContains(t, string(output), `"$ref"`)
	assert.Contains(t, string(output), `"client_device"`)
	assert.Contains(t, string(output), `"device_type"`)
	settingsSchema, err := json.Marshal(tools["update_settings"].InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(settingsSchema), `naming.mdns.interfaces`)
	assert.Contains(t, string(settingsSchema), `dns.bootstrap_dns`)
	assert.Contains(t, string(settingsSchema), `"maxItems":16`)
	upstreamSchema, err := json.Marshal(tools["add_upstream"].InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(upstreamSchema), `"enum":["https","plain"]`)
	assert.Contains(t, string(upstreamSchema), `"default":"plain"`)
}

func TestAdministrationInstructions(t *testing.T) {
	session := connect(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("initialization invoked API")
	}))
	instructions := session.InitializeResult().Instructions
	assert.Contains(t, instructions, "MCP tools first")
	assert.Contains(t, instructions, "add_client")
	assert.Contains(t, instructions, "revision")
	assert.Contains(t, instructions, "read back")
}

func TestClientDeviceCategoriesMatchAdvertisedSchema(t *testing.T) {
	for _, category := range []string{"unknown", "phone", "tablet", "laptop", "desktop", "tv", "speaker", "printer", "camera", "lighting", "appliance", "server", "console"} {
		t.Run(category, func(t *testing.T) {
			// Console observations must remain valid after the catalog is refreshed.
			body := `{"items":[],"observed_available":true,"status":{"active_generation":"1","active_revision":"test","saved_revision":"test","pending":false,"recovered":false,"restart_required":false,"sources":[]},"observed":{"complete":true,"truncated":false,"updated_at":"2026-01-01T00:01:00Z","range":{"from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z"},"items":[{"address":"192.0.2.1","name":"Test device","name_source":"dns-sd","name_fresh":true,"count":"1","blocked":"0","last_seen":"2026-01-01T00:00:30Z","device":{"category":"` + category + `","reason":"Test discovery","inferred":false,"fresh":true,"evidence":[]}}]}}`
			session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			list, err := session.ListTools(t.Context(), nil)
			require.NoError(t, err)
			var output any
			for _, tool := range list.Tools {
				if tool.Name == "list_clients" {
					output = tool.OutputSchema
				}
			}
			require.NotNil(t, output)
			encoded, err := json.Marshal(output)
			require.NoError(t, err)
			var schema jsonschema.Schema
			require.NoError(t, json.Unmarshal(encoded, &schema))
			resolved, err := schema.Resolve(nil)
			require.NoError(t, err)
			result := call(t, session, "list_clients", map[string]any{"limit": 200})
			require.False(t, result.IsError)
			assert.NoError(t, resolved.Validate(result.StructuredContent))
		})
	}
}

func TestPathQueryAndDecimalResults(t *testing.T) {
	requests := make(chan string, 4)
	session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"18446744073709551615","count":9007199254740993}`)
	}))
	result := call(t, session, "get_query", map[string]any{"id": "18446744073709551615"})
	assert.False(t, result.IsError)
	assert.Equal(t, "GET /api/v1/queries/18446744073709551615", <-requests)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"count":9007199254740993`)
	assert.Equal(t, "18446744073709551615", result.StructuredContent.(map[string]any)["id"])
	call(t, session, "list_queries", map[string]any{"name": "a&b.example", "outcome": "blocked"})
	assert.Equal(t, "GET /api/v1/queries?limit=100&name=a%26b.example&outcome=blocked", <-requests)
}

func TestMutationForwardsOnceAndPreservesAPIError(t *testing.T) {
	var calls atomic.Int32
	type request struct{ method, path, body string }
	requests := make(chan request, 1)
	session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		requests <- request{r.Method, r.URL.Path, string(body)}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":"revision_conflict","message":"reload","request_id":"abc","field_errors":[],"active_generation":"9007199254740993"}}`)
	}))
	result := call(t, session, "update_settings", map[string]any{"body": map[string]any{"revision": "opaque-revision", "edits": []any{map[string]any{"path": []string{"cache", "size"}, "value": 100}}}})
	assert.True(t, result.IsError)
	assert.EqualValues(t, 1, calls.Load())
	req := <-requests
	assert.Equal(t, "PATCH", req.method)
	assert.Equal(t, "/api/v1/settings", req.path)
	assert.JSONEq(t, `{"revision":"opaque-revision","edits":[{"path":["cache","size"],"value":100}]}`, req.body)
	assert.Equal(t, "revision_conflict", result.StructuredContent.(map[string]any)["error"].(map[string]any)["code"])
}

func TestInvalidArgumentsNeverInvokeAPI(t *testing.T) {
	var calls atomic.Int32
	session := connect(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	for _, tc := range []struct {
		name string
		args any
	}{
		{"get_query", map[string]any{}},
		{"get_query", map[string]any{"id": "../settings"}},
		{"list_queries", map[string]any{"outcome": "bogus"}},
		{"list_queries", map[string]any{"limit": 201}},
		{"list_queries", map[string]any{"unexpected": true}},
		{"update_settings", map[string]any{"body": map[string]any{"edits": []any{}}}},
		{"create_job", map[string]any{"body": map[string]any{"kind": "upstream-probe"}}},
	} {
		result := call(t, session, tc.name, tc.args)
		assert.True(t, result.IsError, "%s: %v", tc.name, tc.args)
	}
	assert.Zero(t, calls.Load())
}

func TestResponseBoundsAndInvalidJSON(t *testing.T) {
	for _, tc := range []struct{ name, body, code string }{
		{"large", `{"data":"` + strings.Repeat("x", mcpserver.MaxResponseBytes) + `"}`, "response_too_large"},
		{"invalid", `not json`, "invalid_api_response"},
		{"trailing", `{} {}`, "invalid_api_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, tc.body) }))
			result := call(t, session, "get_summary", map[string]any{})
			assert.True(t, result.IsError)
			assert.Equal(t, tc.code, result.StructuredContent.(map[string]any)["error"].(map[string]any)["code"])
			assert.EqualValues(t, 1, calls.Load())
			assert.Less(t, len(result.Content[0].(*mcp.TextContent).Text), 4096)
		})
	}
}

func TestStatelessJSONTransportAndRequestBound(t *testing.T) {
	h, err := mcpserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"queries":"9007199254740993"}`)
	}))
	require.NoError(t, err)
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_summary","arguments":{}}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
		assert.Empty(t, w.Header().Get("Mcp-Session-Id"))
		assert.True(t, json.Valid(w.Body.Bytes()))
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat(" ", mcpserver.MaxRequestBytes+1)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	get := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	get.Header.Set("Accept", "text/event-stream")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, get)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestCommitEscapesIDAndPreservesBodyInteger(t *testing.T) {
	requests := make(chan string, 2)
	session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- r.Method + " " + r.URL.RequestURI() + " " + string(body)
		_, _ = io.WriteString(w, `{}`)
	}))
	result := call(t, session, "commit_configuration", map[string]any{"id": "opaque?value#fragment"})
	assert.False(t, result.IsError)
	assert.Equal(t, "POST /api/v1/config/transactions/opaque%3Fvalue%23fragment/commit ", <-requests)
	result = call(t, session, "update_settings", json.RawMessage(`{"body":{"revision":"r","edits":[{"path":["size"],"value":9007199254740993}]}}`))
	assert.False(t, result.IsError)
	assert.Contains(t, <-requests, `9007199254740993`)
}
