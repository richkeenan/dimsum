package config

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
)

// Only compiled indexes and small metadata survive publication. Compiler inputs
// are transient and durable inputs are read only during recovery.
type subscriptionState struct {
	policy   *policy.PolicySnapshot
	artifact artifactReference
	sources  []SourceStatus
}

func (s *Store) prepareSubscriptions(ctx context.Context, c Config, refresh bool) (*subscriptionState, error) {
	old := s.Snapshot()
	if !refresh && old != nil && reflect.DeepEqual(c.Lists, old.document.value.Lists) {
		if err := s.statSubscriptionArtifact(old.subscriptions.artifact); err != nil {
			return nil, err
		}
		return old.subscriptions, nil
	}
	var reusable map[string]lists.Version
	if !refresh {
		reusable = s.reusableSources(c.Lists)
	}
	fetcher := s.options.Fetcher
	if fetcher == nil {
		var err error
		fetcher, err = lists.NewFetcherWithUpstreams(c.DNS.Upstreams)
		if err != nil {
			return nil, err
		}
		defer fetcher.Client.CloseIdleConnections()
	}
	var rules []policy.Rule
	var statuses []SourceStatus
	for _, sub := range c.Lists {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status := SourceStatus{ID: sub.ID, Enabled: sub.Enabled}
		if sub.Enabled {
			v, reused := reusable[sub.ID]
			var err error
			if !reused {
				v, err = fetcher.Refresh(ctx, filepath.Join(s.state, "sources"), sub, s.options.Offline)
			}
			prior, wasUsable := s.previousSource(sub)
			if !reused && err == nil && wasUsable && !sub.AllowLargeDeletion && len(v.Rules) <= prior.Rules/2 {
				err = fmt.Errorf("source deletion requires review: %d -> %d rules", prior.Rules, len(v.Rules))
			}
			if err != nil && wasUsable {
				warning := err.Error()
				v = lists.Version{SHA256: prior.SHA256, Warning: "retained active source: " + warning}
				for number := uint32(1); ; number++ {
					rule, ok := old.subscriptions.policy.RuleAt(number)
					if !ok {
						break
					}
					if rule.SourceID == sub.ID {
						v.Rules = append(v.Rules, rule)
					}
				}
				if len(v.Rules) != prior.Rules {
					return nil, fmt.Errorf("source %s: active membership missing", sub.ID)
				}
				err = nil
			}
			if err != nil {
				status.Error = err.Error()
			} else {
				if len(v.Rules) > policy.DefaultSnapshotOptions().MaxRules-len(rules) {
					return nil, fmt.Errorf("enabled source rule budget exceeded")
				}
				status.Usable, status.SHA256, status.Rules, status.Error = true, v.SHA256, len(v.Rules), v.Warning
				rules = append(rules, v.Rules...)
			}
		}
		statuses = append(statuses, status)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.compileSubscriptions(c, rules, statuses)
}
