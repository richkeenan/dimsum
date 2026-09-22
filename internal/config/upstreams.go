package config

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/richkeenan/dimsum/internal/upstream"
)

// BindDNSListeners records actual sockets for loop checks on every activation,
// including services whose configured port was zero. It publishes no config.
func (s *Store) BindDNSListeners(addresses []string) error {
	s.build.Lock()
	defer s.build.Unlock()
	snapshot := s.Snapshot()
	if snapshot == nil {
		return fmt.Errorf("service: active configuration required")
	}
	c := snapshot.Config()
	c.DNS.Listen = append([]string(nil), addresses...)
	if err := Validate(c); err != nil {
		return err
	}
	s.listeners = c.DNS.Listen
	return nil
}

// Zero values select the documented upstream defaults. All time values in the
// authoritative text are milliseconds; runtime health is not configuration.
type UpstreamSettings struct {
	Mode             string `yaml:"mode,omitempty"`
	TimeoutMS        int    `yaml:"timeout_ms,omitempty"`
	AttemptTimeoutMS int    `yaml:"attempt_timeout_ms,omitempty"`
	MaxAttempts      int    `yaml:"max_attempts,omitempty"`
	MaxOutstanding   int    `yaml:"max_outstanding,omitempty"`
	FailureThreshold int    `yaml:"failure_threshold,omitempty"`
	OpenMS           int    `yaml:"open_ms,omitempty"`
	MaxBackoffMS     int    `yaml:"max_backoff_ms,omitempty"`
}

func (d DNS) UpstreamOptions() upstream.Options {
	p := d.UpstreamPolicy
	o := upstream.Options{Mode: p.Mode, Timeout: time.Duration(p.TimeoutMS) * time.Millisecond, AttemptTimeout: time.Duration(p.AttemptTimeoutMS) * time.Millisecond, MaxAttempts: p.MaxAttempts, MaxOutstanding: p.MaxOutstanding, FailureThreshold: p.FailureThreshold, OpenInterval: time.Duration(p.OpenMS) * time.Millisecond, MaxBackoff: time.Duration(p.MaxBackoffMS) * time.Millisecond}
	for _, s := range d.Upstreams {
		a, _ := upstream.ParseEndpoint(s)
		o.Endpoints = append(o.Endpoints, a)
	}
	for _, s := range d.Fallback {
		a, _ := upstream.ParseEndpoint(s)
		o.Fallback = append(o.Fallback, a)
	}
	if d.BootstrapDNS != nil {
		o.BootstrapDNS = make([]netip.AddrPort, 0, len(d.BootstrapDNS))
	}
	for _, s := range d.BootstrapDNS {
		a, _ := netip.ParseAddrPort(s)
		o.BootstrapDNS = append(o.BootstrapDNS, a)
	}
	return o
}
