package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"
)

const recoveryVersion = 1
const maxManifestBytes = 4 << 20

type artifactReference struct {
	Name string
	Size int64 // checksummed payload bytes, excluding the artifact envelope
}
type recoveryManifest struct {
	Version    int
	Generation uint64
	Revision   string
	Config     []byte
	Sources    []SourceStatus
	Artifact   artifactReference
}
type subscriptionInputs struct {
	Version int
	Lists   []lists.Subscription
	Rules   []policy.Rule
}

func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("recovery: trailing JSON data")
	}
	return nil
}

func validArtifactName(name string) bool {
	if len(name) != 64+len(".artifact") || !strings.HasSuffix(name, ".artifact") {
		return false
	}
	h := strings.TrimSuffix(name, ".artifact")
	_, err := hex.DecodeString(h)
	return err == nil && h == strings.ToLower(h)
}

func (s *Store) statSubscriptionDirectory() error {
	info, err := os.Lstat(filepath.Join(s.state, "subscriptions"))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("recovery: subscription storage is not a directory")
	}
	return nil
}

func (s *Store) statSubscriptionArtifact(ref artifactReference) error {
	if !validArtifactName(ref.Name) || ref.Size <= 0 || ref.Size > maxRecoveryBytes {
		return fmt.Errorf("recovery: invalid subscription reference")
	}
	if err := s.statSubscriptionDirectory(); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(s.state, "subscriptions", ref.Name))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != ref.Size+48 {
		return fmt.Errorf("recovery: subscription artifact size/type mismatch")
	}
	return nil
}

func validateMembership(c Config, rules []policy.Rule, statuses []SourceStatus) error {
	if len(statuses) != len(c.Lists) {
		return fmt.Errorf("recovery: source/config membership mismatch")
	}
	sources := make(map[string]SourceStatus, len(statuses))
	for i, status := range statuses {
		sub := c.Lists[i]
		if status.ID != sub.ID || status.Enabled != sub.Enabled || status.Rules < 0 {
			return fmt.Errorf("recovery: invalid source status")
		}
		if _, exists := sources[status.ID]; exists {
			return fmt.Errorf("recovery: duplicate source status")
		}
		if status.Usable {
			h, err := hex.DecodeString(status.SHA256)
			if !status.Enabled || status.Rules == 0 || err != nil || len(h) != sha256.Size {
				return fmt.Errorf("recovery: invalid usable source")
			}
		} else if status.Rules != 0 || status.SHA256 != "" {
			return fmt.Errorf("recovery: unusable source has membership")
		}
		sources[status.ID] = status
	}
	counts := make(map[string]int, len(sources))
	for _, rule := range rules {
		status, ok := sources[rule.SourceID]
		prefix := fmt.Sprintf("%d:%s:", len(rule.SourceID), rule.SourceID)
		if !ok || !status.Usable || (rule.Class != policy.SubscriptionAllow && rule.Class != policy.SubscriptionDeny) || !strings.HasPrefix(rule.ID, prefix) {
			return fmt.Errorf("recovery: invalid subscription rule membership")
		}
		counts[rule.SourceID]++
	}
	for id, status := range sources {
		if counts[id] != status.Rules {
			return fmt.Errorf("recovery: source rule count mismatch")
		}
	}
	return nil
}

func (s *Store) compileSubscriptions(c Config, rules []policy.Rule, sources []SourceStatus) (*subscriptionState, error) {
	if err := validateMembership(c, rules, sources); err != nil {
		return nil, err
	}
	compiled, err := policy.CompileSnapshot(1, rules, policy.DefaultLimits())
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(subscriptionInputs{Version: recoveryVersion, Lists: subscriptionMembership(c.Lists), Rules: rules})
	if err != nil {
		return nil, err
	}
	if len(b) > maxRecoveryBytes-maxManifestBytes {
		return nil, fmt.Errorf("recovery: combined artifact budget exceeded")
	}
	ref := artifactReference{Name: fmt.Sprintf("%x.artifact", sha256.Sum256(b)), Size: int64(len(b))}
	path := filepath.Join(s.state, "subscriptions", ref.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := s.statSubscriptionDirectory(); err != nil {
		return nil, err
	}
	// Refresh may produce identical content. Verify existing data before reusing;
	// the ordinary Save path only stats its already validated immutable artifact.
	reuse := false
	if err := s.statSubscriptionArtifact(ref); err == nil {
		existing, err := lists.ReadArtifact(path, maxRecoveryBytes)
		reuse = err == nil && bytes.Equal(existing, b)
	}
	if !reuse {
		// The durable writer renames over the path, replacing a bad file or
		// symlink without following it or modifying its target.
		if err := lists.WriteArtifact(path, b); err != nil {
			return nil, err
		}
	}
	if err := s.statSubscriptionArtifact(ref); err != nil {
		return nil, err
	}
	// WriteArtifact syncs the subscriptions directory; also persist its entry in
	// the state directory before any manifest can reference it.
	if err := syncDirectory(s.state); err != nil {
		return nil, err
	}
	return &subscriptionState{policy: compiled, artifact: ref, sources: sources}, nil
}

func (s *Store) stageManifest(d *Document, generation uint64, subs *subscriptionState) (string, error) {
	m := recoveryManifest{Version: recoveryVersion, Generation: generation, Revision: d.Revision(), Config: d.Bytes(), Sources: subs.sources, Artifact: subs.artifact}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if len(b) > maxManifestBytes || int64(len(b))+m.Artifact.Size > maxRecoveryBytes {
		return "", fmt.Errorf("recovery: combined artifact budget exceeded")
	}
	path := filepath.Join(s.state, "candidate.manifest")
	return path, lists.WriteArtifact(path, b)
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *Store) commitManifest(stage string) error {
	if err := os.Rename(stage, filepath.Join(s.state, "active.manifest")); err != nil {
		return err
	}
	return syncDirectory(s.state)
}

func (s *Store) recover() error {
	path := filepath.Join(s.state, "active.manifest")
	_, statErr := os.Lstat(path)
	legacy := os.IsNotExist(statErr)
	var m recoveryManifest
	var legacyRules []policy.Rule
	if legacy {
		b, err := lists.ReadArtifact(filepath.Join(s.state, "active.artifact"), maxRecoveryBytes)
		if err != nil {
			return err
		}
		var a recovery
		if err := strictJSON(b, &a); err != nil {
			return err
		}
		m = recoveryManifest{Version: recoveryVersion, Generation: a.Generation, Revision: a.Revision, Config: a.Config, Sources: a.Sources}
		legacyRules = a.Rules
	} else {
		if statErr != nil {
			return statErr
		}
		b, err := lists.ReadArtifact(path, maxManifestBytes)
		if err != nil {
			return err
		}
		if err := strictJSON(b, &m); err != nil {
			return err
		}
	}
	if m.Version != recoveryVersion || m.Generation == 0 || m.Revision != revision(m.Config) || len(m.Config) > maxConfigBytes {
		return fmt.Errorf("recovery: invalid version or configuration identity")
	}
	d, err := Parse(m.Config)
	if err != nil {
		return err
	}
	if err := s.validateDocumentSecrets(d); err != nil {
		return err
	}
	var subs *subscriptionState
	if legacy {
		var rules []policy.Rule
		owners := make(map[string]policy.Rule)
		for _, rule := range legacyRules {
			if rule.Class == policy.SubscriptionAllow || rule.Class == policy.SubscriptionDeny {
				rules = append(rules, rule)
			} else {
				if _, exists := owners[rule.ID]; exists {
					return fmt.Errorf("recovery: duplicate owner rule")
				}
				owners[rule.ID] = rule
			}
		}
		expected := d.value.scopedPolicyRules()
		if len(expected) != len(owners) {
			return fmt.Errorf("recovery: owner/config mismatch")
		}
		for _, rule := range expected {
			if owners[rule.ID] != rule {
				return fmt.Errorf("recovery: owner/config mismatch")
			}
		}
		subs, err = s.compileSubscriptions(d.value, rules, m.Sources)
	} else {
		if err := s.statSubscriptionArtifact(m.Artifact); err != nil {
			return err
		}
		if m.Artifact.Size > maxRecoveryBytes-maxManifestBytes {
			return fmt.Errorf("recovery: combined artifact budget exceeded")
		}
		b, e := lists.ReadArtifact(filepath.Join(s.state, "subscriptions", m.Artifact.Name), m.Artifact.Size)
		if e != nil {
			return e
		}
		if int64(len(b)) != m.Artifact.Size || fmt.Sprintf("%x.artifact", sha256.Sum256(b)) != m.Artifact.Name {
			return fmt.Errorf("recovery: subscription content identity mismatch")
		}
		var inputs subscriptionInputs
		if err := strictJSON(b, &inputs); err != nil {
			return err
		}
		if inputs.Version != recoveryVersion || !reflect.DeepEqual(inputs.Lists, subscriptionMembership(d.value.Lists)) {
			return fmt.Errorf("recovery: subscription version/config mismatch")
		}
		if err := validateMembership(d.value, inputs.Rules, m.Sources); err != nil {
			return err
		}
		compiled, e := policy.CompileSnapshot(1, inputs.Rules, policy.DefaultLimits())
		err = e
		subs = &subscriptionState{policy: compiled, artifact: m.Artifact, sources: m.Sources}
	}
	if err != nil {
		return err
	}
	var builtinsChanged bool
	subs, builtinsChanged, err = s.recoverBuiltins(d.value, subs)
	if err != nil {
		return err
	}
	if builtinsChanged {
		m.Generation++
		m.Sources = subs.sources
	}
	clientPolicies, err := d.value.compileClientPolicies(m.Generation, subs.policy)
	if err != nil {
		return err
	}
	local, err := localdns.Build(d.value.Zones, d.value.Records)
	if err != nil {
		return err
	}
	names, err := clients.NewView(d.value.Naming, d.value.NamingOverrides(), local.Names())
	if err != nil {
		return err
	}
	if legacy || builtinsChanged {
		stage, err := s.stageManifest(d, m.Generation, subs)
		if err != nil {
			return err
		}
		defer os.Remove(stage)
		if err := s.commitManifest(stage); err != nil {
			return err
		}
	}
	s.publish(&Snapshot{document: d, policy: clientPolicies.Network().Policy(), clientPolicies: clientPolicies, local: local, names: names, generation: m.Generation, subscriptions: subs}, m.Sources, true)
	s.cleanupSubscriptions(subs.artifact.Name)
	return nil
}

// Best-effort, bounded maintenance. Never traverse directories/symlinks, touch
// unknown names, or remove the durable manifest's reference. Failed candidates
// may leave immutable orphans; startup and refresh reclaim them after commit.
func (s *Store) cleanupSubscriptions(active string) {
	dir := filepath.Join(s.state, "subscriptions")
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return
	}
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	entries, _ := f.ReadDir(1024)
	for _, entry := range entries {
		if entry.Name() == active || !validArtifactName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	_ = syncDirectory(dir)
}
