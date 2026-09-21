package config

import (
	"fmt"
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestValidateAggregateCustomRegexBudget(t *testing.T) {
	c := Default()
	for i := 0; i <= policy.DefaultLimits().MaxRegex; i++ {
		c.Rules = append(c.Rules, CustomRule{ID: fmt.Sprint(i), Action: "deny", Kind: policy.Regex, Pattern: "ads", Enabled: true})
	}
	assert.ErrorContains(t, Validate(c), "regex")
	text, err := yaml.Marshal(c)
	require.NoError(t, err)
	_, err = Parse(text)
	assert.ErrorContains(t, err, "regex")
}
