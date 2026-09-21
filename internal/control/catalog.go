package control

import "github.com/richkeenan/dimsum/internal/catalog"

type CatalogItem struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Description       string `json:"description"`
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
		items = append(items, CatalogItem{e.ID, e.Label, e.Description, e.URL, string(e.Dialect), kind, e.Available, e.UnavailableReason, e.DefaultEnabled})
	}
	return struct {
		Items []CatalogItem `json:"items"`
	}{items}
}
