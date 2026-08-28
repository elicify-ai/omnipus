// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// T25 (ADR-067 FR-011, US-5/US-10/US-11) — TestSeeds_CanonicalProviderIDs
// pins the fresh-install seed (pkg/config/defaults.go) to catalog reality.
//
// The seed's whole job is to hand a fresh install a working template per
// vendor without Go ever knowing a vendor's name. That property breaks
// silently the moment a template's `Provider` drifts from an exact catalog
// id — the row would still compile, still look plausible in a diff, and
// only fail at construction time with ErrUnknownProvider naming the id and
// nothing else (FR-015). This test catches that at build time instead: every
// seed row's Provider MUST be an exact catalog id (trimmed, never
// case-folded — A-19), and no seed row may carry the retired
// `ModelConfig.Custom` / `APIBase` shape a template has no business setting
// (the catalog supplies the URL — FR-012).
func TestSeeds_CanonicalProviderIDs(t *testing.T) {
	cfg := config.DefaultConfig()
	if len(cfg.Providers) == 0 {
		t.Fatal("DefaultConfig().Providers is empty — nothing to assert")
	}
	for _, tpl := range cfg.Providers {
		if tpl == nil {
			t.Fatal("DefaultConfig().Providers contains a nil row")
		}
		if !IsCatalogProvider(tpl.Provider) {
			t.Errorf("seed template provider %q is not a catalog id (ADR-067 FR-011)", tpl.Provider)
		}
		if tpl.Model == "" {
			t.Errorf("seed template for provider %q has an empty Model", tpl.Provider)
		}
		if tpl.Custom {
			t.Errorf("seed template for provider %q sets Custom — a fresh-install template is a catalog row, never an operator-typed custom row", tpl.Provider)
		}
		if tpl.APIBase != "" {
			t.Errorf("seed template for provider %q sets APIBase %q — the catalog supplies the URL (FR-012)", tpl.Provider, tpl.APIBase)
		}
	}
}
