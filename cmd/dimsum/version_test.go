package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionJSON(t *testing.T) {
	var out, errors bytes.Buffer
	require.Zero(t, run(context.Background(), []string{"version"}, &out, &errors))
	var got map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, version, got["version"])
	assert.NotEmpty(t, got["go_version"])
	assert.Empty(t, errors.String())
}
