package config

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
)

type stagedChange struct {
	Expected string
	Config   []byte
}

// Stage validates a complete document outside the watched authoritative file.
// Multiple Document edits are one Save at commit; no multi-rename transaction.
func (s *Store) Stage(expected string, d *Document) (string, error) {
	s.build.Lock()
	defer s.build.Unlock()
	if d == nil {
		return "", fmt.Errorf("stage: nil document")
	}
	if _, err := Parse(d.Bytes()); err != nil {
		return "", err
	}
	if _, err := policy.CompileSnapshot(0, d.Config().PolicyRules(), policy.DefaultLimits()); err != nil {
		return "", err
	}
	b, err := readConfig(s.path)
	if err != nil {
		return "", err
	}
	if revision(b) != expected {
		return "", ErrConflict
	}
	payload, err := json.Marshal(stagedChange{expected, d.Bytes()})
	if err != nil {
		return "", err
	}
	id := revision(payload)
	err = lists.WriteArtifact(filepath.Join(s.state, "stages", id+".stage"), payload)
	return id, err
}
func (s *Store) CommitStage(ctx context.Context, id string) (ActivationResult, error) {
	if len(id) != 64 {
		return s.Inspect(), fmt.Errorf("stage: invalid ID")
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return s.Inspect(), fmt.Errorf("stage: invalid ID")
		}
	}
	b, err := lists.ReadArtifact(filepath.Join(s.state, "stages", id+".stage"), 2*maxConfigBytes)
	if err != nil {
		return s.Inspect(), err
	}
	if revision(b) != id {
		return s.Inspect(), fmt.Errorf("stage: identity mismatch")
	}
	var stage stagedChange
	if err = json.Unmarshal(b, &stage); err != nil {
		return s.Inspect(), err
	}
	d, err := Parse(stage.Config)
	if err != nil {
		return s.Inspect(), err
	}
	return s.Save(ctx, stage.Expected, d)
}
