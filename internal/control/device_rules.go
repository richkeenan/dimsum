package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
)

type DeviceRulesRead struct {
	Revision   string                  `json:"revision"`
	Status     Activation              `json:"status"`
	Entries    []clients.DNSGuessEntry `json:"entries"`
	Customized bool                    `json:"customized"`
}

type DeviceRulesMutation struct {
	Revision string                `json:"revision"`
	Action   string                `json:"action"`
	ID       string                `json:"id,omitempty"`
	Rule     *clients.DNSGuessRule `json:"rule,omitempty"`
	Enabled  *bool                 `json:"enabled,omitempty"`
}

func (s *Service) ReadDeviceRules() (DeviceRulesRead, error) {
	status, err := s.Status()
	if err != nil {
		return DeviceRulesRead{}, err
	}
	d, err := s.documentAt(status.SavedRevision)
	if err != nil {
		return DeviceRulesRead{}, err
	}
	o := d.Config().Naming.DNSGuesses
	entries, err := clients.DNSGuessCatalogue(o)
	return DeviceRulesRead{Revision: status.SavedRevision, Status: status, Entries: entries, Customized: o.Customized()}, err
}

func (s *Service) MutateDeviceRules(ctx context.Context, m DeviceRulesMutation) (Activation, error) {
	if s.options.Store == nil {
		return Activation{}, ErrUnavailable
	}
	if m.Revision == "" {
		return Activation{}, fmt.Errorf("revision is required")
	}
	d, err := s.documentAt(m.Revision)
	if err != nil {
		return Activation{}, err
	}
	o := d.Config().Naming.DNSGuesses
	entries, err := clients.DNSGuessCatalogue(o)
	if err != nil {
		return Activation{}, err
	}
	var entry *clients.DNSGuessEntry
	for i := range entries {
		if entries[i].Rule.ID == m.ID {
			entry = &entries[i]
			break
		}
	}
	custom := slices.IndexFunc(o.Custom, func(r clients.DNSGuessRule) bool { return r.ID == m.ID })
	root := []string{"naming", "dns_guesses"}
	rulePath := []string{"naming", "dns_guesses", "rules", m.ID}
	var fields []config.PolicyField
	switch m.Action {
	case "save":
		if m.Rule == nil || m.ID == "" || m.Rule.ID != m.ID || m.Enabled != nil {
			return Activation{}, fmt.Errorf("save requires matching id and rule, without enabled")
		}
		builtin := entry != nil && entry.Builtin != nil && entry.Origin != "custom"
		rule, e := clients.NormalizeDNSGuessRule(*m.Rule, builtin)
		if e != nil {
			return Activation{}, e
		}
		if builtin {
			delta := deviceRuleDelta(*entry.Builtin, rule, o.Rules[m.ID])
			if delta.IsZero() {
				fields = append(fields, config.PolicyField{Path: rulePath, Reset: true})
			} else {
				fields = append(fields, config.PolicyField{Path: rulePath, Value: delta})
			}
		} else if entry != nil && !entry.Available {
			return Activation{}, fmt.Errorf("baseline rule is no longer available; reset it before creating a custom rule with this id")
		} else if custom >= 0 {
			path := []string{"naming", "dns_guesses", "custom", strconv.Itoa(custom)}
			for _, f := range []struct {
				name  string
				value any
			}{{"name", rule.Name}, {"category", rule.Category}, {"reason", rule.Reason}, {"icon", rule.Icon}, {"domains", rule.Domains}} {
				fields = append(fields, config.PolicyField{Path: appendPath(path, f.name), Value: f.value})
			}
		} else {
			fields = append(fields, config.PolicyField{Path: appendPath(root, "custom"), Value: rule, Append: true})
		}
	case "enable":
		if m.Rule != nil || m.Enabled == nil || m.ID == "" {
			return Activation{}, fmt.Errorf("enable requires id and enabled, without rule")
		}
		if entry == nil || !entry.Available {
			return Activation{}, NotFound
		}
		fields = append(fields, config.PolicyField{Path: appendPath(rulePath, "disabled"), Value: !*m.Enabled})
	case "delete", "reset":
		if m.Rule != nil || m.Enabled != nil {
			return Activation{}, fmt.Errorf("reset/delete do not accept rule or enabled")
		}
		if m.ID == "" {
			if m.Action != "reset" {
				return Activation{}, fmt.Errorf("delete requires id")
			}
			// Retain a mapping anchor so reset preserves owner comments.
			fields = append(fields, config.PolicyField{Path: root, Value: clients.DNSGuessOverrides{}})
		} else {
			if entry == nil {
				return Activation{}, NotFound
			}
			if m.Action == "delete" && custom < 0 {
				return Activation{}, fmt.Errorf("built-in rules can be disabled or reset, not deleted")
			}
			if custom >= 0 {
				d, err = d.RemovePreservingComments([]string{"naming", "dns_guesses", "custom", strconv.Itoa(custom)})
				if err != nil {
					return Activation{}, err
				}
			}
			fields = append(fields, config.PolicyField{Path: rulePath, Reset: true})
		}
	default:
		return Activation{}, fmt.Errorf("action must be save, enable, delete, or reset")
	}
	next, err := d.PolicyFields(fields)
	if err != nil {
		return Activation{}, err
	}
	if err = s.checkDHCPAvailability(next); err != nil {
		return Activation{}, err
	}
	a, err := s.options.Store.Save(ctx, m.Revision, next)
	return activation(a), err
}

func deviceRuleDelta(base, rule clients.DNSGuessRule, previous clients.DNSGuessOverride) clients.DNSGuessOverride {
	o := clients.DNSGuessOverride{Disabled: previous.Disabled}
	for _, field := range []struct {
		base, value string
		target      **string
	}{
		{base.Name, rule.Name, &o.Name}, {base.Category, rule.Category, &o.Category},
		{base.Reason, rule.Reason, &o.Reason}, {base.Icon, rule.Icon, &o.Icon},
	} {
		if field.value != field.base {
			value := field.value
			*field.target = &value
		}
	}
	for _, domain := range rule.Domains {
		if !slices.Contains(base.Domains, domain) || slices.Contains(previous.Additions, domain) {
			o.Additions = append(o.Additions, domain)
		}
	}
	for _, domain := range append(slices.Clone(base.Domains), previous.Exclusions...) {
		if !slices.Contains(rule.Domains, domain) && !slices.Contains(o.Exclusions, domain) {
			o.Exclusions = append(o.Exclusions, domain)
		}
	}
	slices.Sort(o.Additions)
	slices.Sort(o.Exclusions)
	return o
}
