package policy

import "fmt"

type ScopeKind string

const (
	NetworkScope ScopeKind = ""
	ProfileScope ScopeKind = "profile"
	ClientScope  ScopeKind = "client"
)

// Scope identifies the owner of a custom rule. The zero value is network-wide.
type Scope struct {
	Kind ScopeKind `yaml:"kind,omitempty" json:"kind,omitempty"`
	ID   string    `yaml:"id,omitempty" json:"id,omitempty"`
}

func (s Scope) Validate() error {
	if s.Kind == NetworkScope && s.ID == "" || (s.Kind == ProfileScope || s.Kind == ClientScope) && s.ID != "" {
		return nil
	}
	return fmt.Errorf("invalid rule scope")
}

// Selection is an immutable source allow-mask and owner context. Nil selection
// preserves legacy subscription eligibility; an empty source list selects none.
type Selection struct {
	profile, client string
	sources         map[string]bool
}

func NewSelection(profile, client string, sources []string) *Selection {
	s := &Selection{profile: profile, client: client, sources: make(map[string]bool, len(sources))}
	for _, id := range sources {
		s.sources[id] = true
	}
	return s
}

// WithSelection is a constant-size view: all rule/index arenas remain shared.
func (s *PolicySnapshot) WithSelection(selection *Selection) *PolicySnapshot {
	v := *s
	v.selection = selection
	v.noFallback = !s.hasEligibleFallback(selection)
	if s.base != nil {
		v.noBaseFallback = !s.base.hasEligibleFallback(selection)
	}
	return &v
}

func (s *PolicySnapshot) hasEligibleFallback(selection *Selection) bool {
	for _, number := range s.fallbackIDs {
		r := s.rules[number-1]
		if selection.eligible(s.scope(r), snapshotClasses[r.class], s.text(s.sharedText[r.source])) {
			return true
		}
	}
	return false
}

func scopeRank(s ScopeKind) int {
	switch s {
	case ClientScope:
		return 2
	case ProfileScope:
		return 1
	}
	return 0
}

func (s *Selection) eligible(scope Scope, class Class, source string) bool {
	switch scope.Kind {
	case ClientScope:
		if s == nil || scope.ID != s.client {
			return false
		}
	case ProfileScope:
		if s == nil || scope.ID != s.profile {
			return false
		}
	}
	return s == nil || class != SubscriptionAllow && class != SubscriptionDeny || s.sources[source]
}

func validateRuleScope(r Rule) error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.Scope.Kind != NetworkScope && r.Class != CustomAllow && r.Class != CustomDeny {
		return fmt.Errorf("only custom rules can have owner scope")
	}
	return nil
}
