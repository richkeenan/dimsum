package control

import "github.com/richkeenan/dimsum/internal/catalog"

type CatalogItem struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Description       string `json:"description"`
	Category          string `json:"category,omitempty"`
	URL               string `json:"url"`
	Dialect           string `json:"dialect"`
	DomainKind        string `json:"domain_kind"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason"`
	DefaultEnabled    bool   `json:"default_enabled"`
}

func (s *Service) Catalog() any {
	entries := catalog.Entries()
	items := make([]CatalogItem, 0, len(entries))
	for _, e := range entries {
		kind := string(e.Source().DomainKind)
		items = append(items, CatalogItem{
			ID: e.ID, Label: e.Label, Description: e.Description, Category: e.Category,
			URL: e.URL, Dialect: string(e.Dialect), DomainKind: kind,
			Available: e.Available, UnavailableReason: e.UnavailableReason, DefaultEnabled: e.DefaultEnabled,
		})
	}
	return struct {
		Items []CatalogItem `json:"items"`
	}{items}
}
