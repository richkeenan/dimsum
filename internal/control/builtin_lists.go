package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/lists"
)

type BuiltinListRead struct {
	ID         string               `json:"id"`
	Revision   string               `json:"revision"`
	Status     Activation           `json:"status"`
	Entries    []lists.BuiltinEntry `json:"entries"`
	Customized bool                 `json:"customized"`
}

type BuiltinListMutation struct {
	Revision string `json:"revision"`
	Action   string `json:"action"`
	Domain   string `json:"domain,omitempty"`
}

func builtinSubscription(d *config.Document, id string) (lists.Subscription, int, error) {
	for i, sub := range d.Config().Lists {
		if sub.ID == id && sub.URL == lists.WorkCompatibilityURL {
			return sub, i, nil
		}
	}
	return lists.Subscription{}, 0, NotFound
}

func (s *Service) ReadBuiltinList(id string) (BuiltinListRead, error) {
	status, err := s.Status()
	if err != nil {
		return BuiltinListRead{}, err
	}
	d, err := s.documentAt(status.SavedRevision)
	if err != nil {
		return BuiltinListRead{}, err
	}
	sub, _, err := builtinSubscription(d, id)
	if err != nil {
		return BuiltinListRead{}, err
	}
	entries, err := lists.BuiltinEntries(sub)
	customized := sub.BuiltinOverrides != nil && len(sub.BuiltinOverrides.Additions)+len(sub.BuiltinOverrides.Exclusions) > 0
	return BuiltinListRead{ID: id, Revision: status.SavedRevision, Status: status, Entries: entries, Customized: customized}, err
}

func (s *Service) MutateBuiltinList(ctx context.Context, id string, m BuiltinListMutation) (Activation, error) {
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
	sub, index, err := builtinSubscription(d, id)
	if err != nil {
		return Activation{}, err
	}
	o := lists.BuiltinOverrides{}
	if sub.BuiltinOverrides != nil {
		o.Additions = slices.Clone(sub.BuiltinOverrides.Additions)
		o.Exclusions = slices.Clone(sub.BuiltinOverrides.Exclusions)
	}
	if m.Action == "reset" {
		if m.Domain != "" {
			return Activation{}, fmt.Errorf("reset does not accept domain")
		}
		o = lists.BuiltinOverrides{}
	} else {
		domain, e := lists.NormalizeBuiltinDomain(m.Domain)
		if e != nil {
			return Activation{}, e
		}
		baseline := sub
		baseline.BuiltinOverrides = nil
		entries, e := lists.BuiltinEntries(baseline)
		if e != nil {
			return Activation{}, e
		}
		builtin := slices.ContainsFunc(entries, func(v lists.BuiltinEntry) bool { return v.Domain == domain })
		remove := func(v []string) []string { return slices.DeleteFunc(v, func(s string) bool { return s == domain }) }
		switch m.Action {
		case "add":
			o.Exclusions = remove(o.Exclusions)
			if !builtin && !slices.Contains(o.Additions, domain) {
				o.Additions = append(o.Additions, domain)
			}
		case "remove":
			if !builtin && !slices.Contains(o.Additions, domain) && !slices.Contains(o.Exclusions, domain) {
				return Activation{}, NotFound
			}
			o.Additions = remove(o.Additions)
			if builtin && !slices.Contains(o.Exclusions, domain) {
				o.Exclusions = append(o.Exclusions, domain)
			}
		case "restore":
			if !builtin && !slices.Contains(o.Exclusions, domain) {
				return Activation{}, NotFound
			}
			o.Exclusions = remove(o.Exclusions)
		default:
			return Activation{}, fmt.Errorf("action must be add, remove, restore, or reset")
		}
	}
	slices.Sort(o.Additions)
	slices.Sort(o.Exclusions)
	f := config.PolicyField{Path: []string{"lists", strconv.Itoa(index), "builtin_overrides"}, Value: o}
	// Keep an existing empty mapping as an edit anchor. Removing its key would
	// strand preserved owner comments at the subscription's append boundary.
	if sub.BuiltinOverrides == nil && len(o.Additions)+len(o.Exclusions) == 0 {
		f.Value = nil
		f.Reset = true
	}
	next, err := d.PolicyFields([]config.PolicyField{f})
	if err != nil {
		return Activation{}, err
	}
	if err = s.checkDHCPAvailability(next); err != nil {
		return Activation{}, err
	}
	a, err := s.options.Store.Save(ctx, m.Revision, next)
	return activation(a), err
}
