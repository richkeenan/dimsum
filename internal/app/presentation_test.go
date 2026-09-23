package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryFilterAcceptsObservedHyphens(t *testing.T) {
	wire, err := presentationWire("r1---edge.example")
	require.NoError(t, err)
	assert.Equal(t, []byte("\x09r1---edge\x07example\x00"), wire)
}
