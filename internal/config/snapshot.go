package config

import (
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"
)

// Snapshot is immutable. A request loads it once and holds that pointer through
// all name checks. No generation registry retains retired snapshots.
type Snapshot struct {
	document   *Document
	policy     *policy.PolicySnapshot
	generation uint64
	local      *localdns.Zones
}

func (s *Snapshot) Config() Config                 { return s.document.Config() }
func (s *Snapshot) Policy() *policy.PolicySnapshot { return s.policy }
func (s *Snapshot) Local() *localdns.Zones         { return s.local }
func (s *Snapshot) Generation() uint64             { return s.generation }
func (s *Snapshot) Revision() string               { return s.document.Revision() }

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
