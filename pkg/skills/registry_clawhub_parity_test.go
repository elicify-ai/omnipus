// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRegistryManagerFromConfig_IncludesClawHub proves the agent-side path: a
// RegistryManager built from a (healed) default config — ClawHub enabled with a
// BaseURL — includes a "clawhub" registry and SearchAll queries it. This is the
// agent↔UI parity guarantee: the agent's find_skills/install_skill reach the
// SAME ClawHub the UI's GET /skills/search reaches.
//
// Uses a stub HTTP server, NOT the real clawhub.ai.
func TestRegistryManagerFromConfig_IncludesClawHub(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v1/search" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		slug := "github"
		name := "GitHub Integration"
		summary := "Interact with GitHub repos"
		version := "1.0.0"
		_ = json.NewEncoder(w).Encode(clawhubSearchResponse{
			Results: []clawhubSearchResult{
				{Score: 0.95, Slug: &slug, DisplayName: &name, Summary: &summary, Version: &version},
			},
		})
	}))
	defer srv.Close()

	// Mirror what the agent loop passes after config.restoreSkillDiscoveryDefaults
	// has healed an onboarded config: ClawHub enabled, BaseURL set (the loop also
	// defaults an empty URL to https://clawhub.ai, but in tests we point at the
	// stub server).
	mgr := NewRegistryManagerFromConfig(RegistryConfig{
		MaxConcurrentSearches: 2,
		ClawHub: ClawHubConfig{
			Enabled: true,
			BaseURL: srv.URL,
		},
	})

	if reg := mgr.GetRegistry("clawhub"); reg == nil {
		t.Fatalf("RegistryManager has no clawhub registry; agent find_skills would reach ZERO registries")
	}

	results, err := mgr.SearchAll(context.Background(), "github", 10)
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if hits == 0 {
		t.Fatalf("stub ClawHub server was never queried; SearchAll did not include ClawHub")
	}
	if len(results) != 1 {
		t.Fatalf("SearchAll returned %d results; want 1 from ClawHub", len(results))
	}
	if results[0].Slug != "github" || results[0].RegistryName != "clawhub" {
		t.Fatalf("result = {slug:%q registry:%q}; want {github clawhub}", results[0].Slug, results[0].RegistryName)
	}
}

// TestRegistryManagerFromConfig_SkipsExplicitlyDisabledClawHub confirms the
// deny path is intact: an operator who explicitly disabled ClawHub gets a
// manager with no ClawHub registry (SearchAll has nothing to query).
func TestRegistryManagerFromConfig_SkipsExplicitlyDisabledClawHub(t *testing.T) {
	mgr := NewRegistryManagerFromConfig(RegistryConfig{
		ClawHub: ClawHubConfig{
			Enabled: false,
			BaseURL: "https://clawhub.ai",
		},
	})
	if reg := mgr.GetRegistry("clawhub"); reg != nil {
		t.Fatalf("clawhub registry present despite Enabled=false; deny-by-explicit-operator-intent broken")
	}
	if _, err := mgr.SearchAll(context.Background(), "github", 10); err == nil {
		t.Fatalf("SearchAll with no registries should error; got nil")
	}
}
