package gateway

// RED for issue #1056 F-2 / amended ADR-096 D10: full SearXNG removal
// includes both the actual settings response and its catalogue source.
// The tests exercise real list-building code, not a test-only UI filter.

import "testing"

func TestFixF2_IntegrationsResponseOmitsRemovedSearXNG(t *testing.T) {
	api, user, _ := newRolesTestAPI(t)
	reauthFor(t, api, user)
	resp := getIntegrationList(t, api, user)
	for _, row := range resp.Search {
		if string(row.Id) == "searxng" {
			t.Fatalf("removed SearXNG still appears in Settings search list: %+v", row)
		}
	}
}

func TestFixF2_IntegrationsCatalogueOmitsRemovedSearXNG(t *testing.T) {
	for _, def := range integrationCatalogue() {
		if def.id == "searxng" {
			t.Fatalf("removed SearXNG still appears in integrationCatalogue: %+v", def)
		}
	}
}
