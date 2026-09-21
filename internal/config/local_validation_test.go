package config_test

import (
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestNamingResolverCannotLoopOrUsePublicFallback(t *testing.T) {
	for _, endpoint := range []string{"127.0.0.1:5353", "1.1.1.1:53", "[::ffff:127.0.0.1]:5353"} {
		c := config.Default()
		c.Naming = clients.Settings{Resolver: endpoint}
		assert.Error(t, config.Validate(c))
	}
	c := config.Default()
	c.Naming = clients.Settings{Resolver: "192.168.1.1:53"}
	assert.NoError(t, config.Validate(c))
}
