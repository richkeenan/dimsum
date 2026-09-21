package control

import (
	"context"
	"net/url"
)

// ClientProvider is an optional extension; existing history providers need not
// implement observed naming. Configured overrides always remain in items.
type ClientProvider interface {
	Clients(context.Context, url.Values) (any, error)
}

func (s *Service) Clients(ctx context.Context, q url.Values) (any, error) {
	configured, e := s.Inspect("clients")
	if e != nil {
		return nil, e
	}
	result := configured.(map[string]any)
	result["observed_available"] = false
	provider, ok := s.options.Provider.(ClientProvider)
	if !ok {
		return result, nil
	}
	observed, e := provider.Clients(ctx, q)
	if e != nil {
		return nil, e
	}
	result["observed_available"] = true
	result["observed"] = observed
	return result, nil
}
