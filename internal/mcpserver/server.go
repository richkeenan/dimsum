// Package mcpserver adapts explicitly advertised OpenAPI operations to MCP tools.
// It owns no listener, credentials, or persistent client sessions.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/richkeenan/dimsum/api"
)

// MaxResponseBytes bounds the captured API JSON body. MCP includes this JSON as
// both structured content and text, so the wire result can be larger than this.
const MaxResponseBytes = 1 << 20

// MaxRequestBytes bounds a complete incoming MCP request, including arguments.
const MaxRequestBytes = 1 << 20

// New builds a stateless, JSON-only Streamable HTTP adapter. handler must be the
// existing authenticated-local API route, not the outer admin router. The caller
// must enforce authentication, Host/Origin checks, admission and request deadlines
// around this handler on every /mcp request, including initialized clients.
func New(handler http.Handler) (http.Handler, error) {
	if handler == nil {
		return nil, errors.New("MCP API handler is nil")
	}
	document, err := api.JSON()
	if err != nil {
		return nil, err
	}
	operations, err := compile(document)
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "dimsum", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: `Use dimsum MCP tools first for administration of this running DNS service: device names, blocking, rules, records, settings, and diagnostics. These are routine operational actions, not software-development tasks.
For a device rename, use list_clients to inspect configured overrides and observations. Use add_client with an address and name for a new override, or update_clients for an existing override. Read the current saved_revision before a mutation, use it as revision, and read back the result to verify the saved name and activation status. On a revision conflict, reread current configuration before retrying.
For device policy such as adding the NSFW list to a phone, inspect list_clients and get_catalog, select the explicit configured ID, then use get_client_policy and update_client_policy. Never guess when a name is unknown or matches multiple devices; ask for explicit selection. For an observed device with an authoritative_mac, create a stable ID using lease_address, which saves MAC-only selectors. Otherwise require explicit address/CIDR/MAC selectors. Relink with selectors or lease_address while retaining ID and policy. A new subscription and its target list override belong in one update_client_policy transaction; new subscriptions have network default application off. preview_client_policy does not save or download. Read back activation and sources.usable/error: assignment alone is not proof of protection. Policy lives in authoritative YAML, not the observations database.
Use get_settings to inspect authoritative configuration and activation status. Use SSH, CLI, or direct file edits only when MCP cannot perform the requested task, explaining the limitation first, or when explicitly requested.
After a server update, reconnect and refresh tool definitions. If structured-result validation fails, compare the freshly advertised schema with the response rather than bypassing validation.`,
	})
	for _, op := range operations {
		server.AddTool(op.tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return op.call(ctx, handler, req.Params.Arguments), nil
		})
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Outer admin has the authoritative Host/Origin policy, including remote LAN hosts.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        MaxRequestBytes,
	}), nil
}

type operation struct {
	path, method string
	tool         *mcp.Tool
	parameters   []parameter
	validation   *jsonschema.Resolved
	hasBody      bool
}

type parameter struct {
	name, location string
	defaultValue   json.RawMessage
}

func (op operation) call(ctx context.Context, handler http.Handler, raw json.RawMessage) *mcp.CallToolResult {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return toolError("invalid_arguments", "Arguments must be a JSON object")
	}
	for _, p := range op.parameters {
		if _, present := args[p.name]; !present && p.defaultValue != nil {
			args[p.name] = p.defaultValue
		}
	}
	// Validate a separate representation: forwarding the original raw values avoids
	// rounding integers above 2^53 in configuration bodies or API results.
	encoded, err := json.Marshal(args)
	if err != nil {
		return toolError("invalid_arguments", err.Error())
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		return toolError("invalid_arguments", err.Error())
	}
	if err := op.validation.Validate(instance); err != nil {
		return toolError("invalid_arguments", err.Error())
	}
	path := op.path
	query := url.Values{}
	for _, p := range op.parameters {
		value, present := args[p.name]
		if !present {
			continue
		}
		text := string(value)
		if len(value) > 0 && value[0] == '"' {
			if err := json.Unmarshal(value, &text); err != nil {
				return toolError("invalid_arguments", err.Error())
			}
		}
		if p.location == "path" {
			// Prevent URL/router normalization from turning an opaque ID into a new route.
			if text == "." || text == ".." || strings.ContainsAny(text, "/\\") {
				return toolError("invalid_arguments", "Path parameters must be a single non-dot segment")
			}
			path = strings.ReplaceAll(path, "{"+p.name+"}", url.PathEscape(text))
		} else {
			query.Set(p.name, text)
		}
	}
	target := "http://localhost" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, op.method, target, bytes.NewReader(args["body"]))
	if err != nil {
		return toolError("invalid_arguments", err.Error())
	}
	req.Header.Set("Accept", "application/json")
	if op.hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := ctx.Err(); err != nil {
		return toolError("request_cancelled", err.Error())
	}
	w := &boundedResponse{header: make(http.Header)}
	// Exactly one invocation: never replay a mutation after an error or overflow.
	handler.ServeHTTP(w, req)
	if w.overflow {
		return toolError("response_too_large", "API response exceeds the MCP response limit; narrow the request. A mutation may already have completed; inspect state before retrying.")
	}
	body := w.body.Bytes()
	if !json.Valid(body) {
		return toolError("invalid_api_response", "API returned an invalid JSON response. A mutation may already have completed; inspect state before retrying.")
	}
	return jsonResult(body, w.status >= 400)
}

func jsonResult(body []byte, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError:           isError,
		StructuredContent: json.RawMessage(body),
		Content:           []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}
}

func toolError(code, message string) *mcp.CallToolResult {
	// Validation errors can include input fragments; keep even those results bounded.
	if len(message) > 2048 {
		message = message[:2048] + "…"
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return jsonResult(body, true)
}

type boundedResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *boundedResponse) Header() http.Header { return w.header }
func (w *boundedResponse) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
}
func (w *boundedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow || len(p) > MaxResponseBytes-w.body.Len() {
		w.overflow = true
		return 0, errors.New("MCP API response limit exceeded")
	}
	return w.body.Write(p)
}

func compile(document []byte) ([]operation, error) {
	var root map[string]any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, err
	}
	paths := object(root["paths"])
	if paths == nil {
		return nil, errors.New("OpenAPI paths missing")
	}
	var operations []operation
	names := map[string]bool{}
	for path, value := range paths {
		item := object(value)
		for method, value := range object(item["x-mcp-tools"]) {
			if excluded(path) {
				return nil, fmt.Errorf("forbidden MCP path %s", path)
			}
			metadata := object(value)
			name, _ := metadata["name"].(string)
			description, _ := metadata["description"].(string)
			if name == "" || description == "" || names[name] {
				return nil, fmt.Errorf("invalid or duplicate MCP metadata for %s %s", method, path)
			}
			names[name] = true
			op, err := compileOperation(root, item, path, method, name, description, metadata)
			if err != nil {
				return nil, fmt.Errorf("MCP tool %s: %w", name, err)
			}
			operations = append(operations, op)
		}
	}
	if len(operations) == 0 {
		return nil, errors.New("OpenAPI has no MCP tools")
	}
	return operations, nil
}

// Credentials, browser sessions, streams and binary archives are never tools,
// even if mistakenly opted in via metadata. New operations default to excluded.
func excluded(path string) bool {
	return !strings.HasPrefix(path, "/api/v1/") || path == "/api/v1/password" ||
		path == "/api/v1/events" || strings.HasPrefix(path, "/api/v1/tokens") ||
		strings.HasPrefix(path, "/api/v1/config/backups/") || strings.HasPrefix(path, "/api/v1/openapi.")
}

func compileOperation(root, item map[string]any, path, method, name, description string, metadata map[string]any) (operation, error) {
	op := operation{path: path, method: strings.ToUpper(method)}
	switch method {
	case "get", "post", "put", "patch", "delete":
	default:
		return op, errors.New("unsupported HTTP method")
	}
	definition := object(item[method])
	if definition == nil {
		return op, errors.New("missing OpenAPI operation")
	}
	if extra, _ := definition["description"].(string); extra != "" {
		description += ". " + extra
	}
	properties := map[string]any{}
	required := []string{}
	input := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	// Operation-level parameters override matching path-level parameters.
	params := map[string]map[string]any{}
	for _, source := range []map[string]any{item, definition} {
		list, _ := source["parameters"].([]any)
		for _, p := range list {
			resolved, err := expand(root, p, 0)
			if err != nil {
				return op, err
			}
			param := object(resolved)
			key := fmt.Sprint(param["in"], ":", param["name"])
			params[key] = param
		}
	}
	for _, param := range params {
		name, _ := param["name"].(string)
		location, _ := param["in"].(string)
		if name == "" || name == "body" || properties[name] != nil {
			return op, errors.New("ambiguous parameter name")
		}
		if location != "query" && location != "path" {
			return op, errors.New("unsupported parameter location")
		}
		schema := object(param["schema"])
		if schema == nil {
			return op, errors.New("parameter schema missing")
		}
		switch schema["type"] {
		case "string", "integer", "number", "boolean":
		default:
			return op, errors.New("only scalar path/query parameters are supported")
		}
		if description, ok := param["description"]; ok {
			schema["description"] = description
		}
		properties[name] = schema
		if param["required"] == true || location == "path" {
			required = append(required, name)
		}
		p := parameter{name: name, location: location}
		if value, present := schema["default"]; present {
			p.defaultValue, _ = json.Marshal(value)
		}
		op.parameters = append(op.parameters, p)
	}
	if raw, present := definition["requestBody"]; present {
		resolved, err := expand(root, raw, 0)
		if err != nil {
			return op, err
		}
		body := object(resolved)
		media := object(object(body["content"])["application/json"])
		if media["schema"] == nil {
			return op, errors.New("only JSON request bodies are supported")
		}
		properties["body"] = media["schema"]
		op.hasBody = true
		if body["required"] == true {
			required = append(required, "body")
		}
	}
	if len(required) > 0 {
		sort.Strings(required)
		input["required"] = required
	}
	sort.Slice(op.parameters, func(i, j int) bool { return op.parameters[i].name < op.parameters[j].name })
	encoded, err := json.Marshal(input)
	if err != nil {
		return op, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return op, err
	}
	op.validation, err = schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return op, err
	}
	readOnly := method == "get" || metadata["readOnly"] == true
	destructive := !readOnly
	op.tool = &mcp.Tool{Name: name, Description: description, InputSchema: json.RawMessage(encoded), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive}}
	// Advertise documented success shapes. API error bodies are returned unchanged
	// with isError, where MCP does not require conformance to the success schema.
	responses := object(definition["responses"])
	var statuses []string
	for status := range responses {
		if strings.HasPrefix(status, "2") {
			statuses = append(statuses, status)
		}
	}
	sort.Strings(statuses)
	var outputs []any
	for _, status := range statuses {
		response, err := expand(root, responses[status], 0)
		if err != nil {
			return op, err
		}
		content := object(object(response)["content"])
		if len(content) == 0 {
			continue
		}
		media := object(content["application/json"])
		if media == nil {
			return op, errors.New("only JSON response bodies may be exposed as MCP tools")
		}
		if schema := media["schema"]; schema != nil {
			outputs = append(outputs, schema)
		}
	}
	if len(outputs) == 1 {
		output := object(outputs[0])
		if output == nil || (output["type"] != nil && output["type"] != "object") {
			return op, errors.New("MCP output schemas must describe objects")
		}
		// MCP requires an explicit object root even when OpenAPI describes the
		// object using anyOf/allOf. Preserve those constraints for result validation.
		output["type"] = "object"
		op.tool.OutputSchema = output
	}
	if len(outputs) > 1 {
		op.tool.OutputSchema = map[string]any{"type": "object", "anyOf": outputs}
	}
	return op, nil
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }

// expand resolves only local JSON pointers, preserving OpenAPI 3.1 $ref siblings.
// A cycle/depth limit fails construction rather than fetching remote references.
func expand(root map[string]any, value any, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("OpenAPI reference cycle or excessive depth")
	}
	switch value := value.(type) {
	case map[string]any:
		result := map[string]any{}
		if raw, exists := value["$ref"]; exists {
			ref, ok := raw.(string)
			if !ok || !strings.HasPrefix(ref, "#/") {
				return nil, errors.New("only local OpenAPI references are supported")
			}
			var target any = root
			for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
				target = object(target)[part]
			}
			if target == nil {
				return nil, fmt.Errorf("unresolved OpenAPI reference %s", ref)
			}
			resolved, err := expand(root, target, depth+1)
			if err != nil {
				return nil, err
			}
			for key, val := range object(resolved) {
				result[key] = val
			}
		}
		for key, val := range value {
			if key == "$ref" {
				continue
			}
			resolved, err := expand(root, val, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	case []any:
		result := make([]any, len(value))
		for i, val := range value {
			resolved, err := expand(root, val, depth+1)
			if err != nil {
				return nil, err
			}
			result[i] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}
