package app_test

import (
	"context"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestUnmanagedRejectsIgnoredPolicy(t *testing.T) {
	for _, set := range []func(*config.Config){
		func(c *config.Config) { c.Blocking = new(bool) },
		func(c *config.Config) { c.Profiles = []config.Profile{{ID: "restricted"}} },
		func(c *config.Config) { c.Zones = []localdns.Zone{{Name: "home.arpa"}} },
		func(c *config.Config) { c.Filtering = policy.Settings{MozillaCanary: true} },
		func(c *config.Config) { c.Clients = []config.ClientOverride{{Address: "192.168.1.2", Name: "device"}} },
		func(c *config.Config) { c.Naming = clients.Settings{Resolver: "192.168.1.1:53"} },
	} {
		c := config.Default()
		c.DNS.Listen = []string{"127.0.0.1:0"}
		c.Admin.Listen = "127.0.0.1:0"
		c.DNS.Upstreams = []string{"127.0.0.1:9"}
		set(&c)
		service := new(app.Service)
		err := service.StartForwarding(context.Background(), c)
		service.Close()
		assert.Error(t, err)
	}
}
