// Package api embeds the authoritative control API description.
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// YAML is the authoritative embedded OpenAPI YAML document. Treat it as read-only.
//
//go:embed openapi.yaml
var YAML []byte

// JSON returns the OpenAPI document as JSON, expanding YAML aliases and merges.
func JSON() ([]byte, error) {
	var document map[string]any
	if err := yaml.Unmarshal(YAML, &document); err != nil {
		return nil, fmt.Errorf("parse OpenAPI: %w", err)
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI: %w", err)
	}
	return data, nil
}
