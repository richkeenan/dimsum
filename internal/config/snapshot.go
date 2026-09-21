package config

import (
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/upstream"
)

// Snapshot is immutable. A request loads it once and holds that pointer through
// all name checks. No generation registry retains retired snapshots.
type Snapshot struct {
	document   *Document
	policy     *policy.PolicySnapshot
	generation uint64
	local      *localdns.Zones
	names      *clients.View
}

func (s *Snapshot) Config() Config                 { return s.document.Config() }
func (s *Snapshot) Policy() *policy.PolicySnapshot { return s.policy }
func (s *Snapshot) Local() *localdns.Zones         { return s.local }
func (s *Snapshot) Names() *clients.View           { return s.names }
func (s *Snapshot) Filtering() policy.Settings     { return s.document.value.Filtering }
func (s *Snapshot) CacheSettings() Cache           { return s.document.value.Cache }
func (s *Snapshot) Generation() uint64             { return s.generation }
func (s *Snapshot) Revision() string               { return s.document.Revision() }

// UpstreamOptions returns owned endpoint slices from the captured generation.
func (s *Snapshot) UpstreamOptions() upstream.Options { return s.document.value.DNS.UpstreamOptions() }

type SourceStatus struct {
	ID      string `json:"id"`
	SHA256  string `json:"sha256,omitempty"`
	Error   string `json:"error,omitempty"`
	Enabled bool   `json:"enabled"`
	Usable  bool   `json:"usable"`
	Rules   int    `json:"rules"`
}
type ActivationResult struct {
	SavedRevision         string         `json:"saved_revision"`
	ActiveRevision        string         `json:"active_revision"`
	ActiveGeneration      uint64         `json:"active_generation"`
	Pending               bool           `json:"pending"`
	Recovered             bool           `json:"recovered"`
	RestartRequired       bool           `json:"restart_required"`
	SubscriptionAvailable bool           `json:"subscription_available"`
	Error                 string         `json:"error,omitempty"`
	Sources               []SourceStatus `json:"sources"`
}
