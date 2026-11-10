// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-096 D14 / spec "Exa" section, Key injection row: Exa joins the
// enabled-but-keyless warning list
// (pkg/tools/web.go::enabledButKeylessSearchProviders) when its ref joins
// nonChannelRefsFor. The warning fires at tool construction before
// selection, so an enabled-but-keyless provider is named before the chain
// picks whoever is left.
//
// This is a new file, not an edit to web_test.go, so it cannot collide with
// parallel lanes' edits there.

package tools

import (
	"reflect"
	"sort"
	"testing"
)

// containsExa reports whether the keyless list names exa.
func containsExa(list []misconfiguredSearchProvider) bool {
	for _, m := range list {
		if m.name == "exa" {
			return true
		}
	}
	return false
}

func TestEnabledButKeylessSearchProviders_IncludesExa(t *testing.T) {
	t.Run("listed when enabled and keyless", func(t *testing.T) {
		got := enabledButKeylessSearchProviders(WebSearchToolOptions{ExaEnabled: true})
		if !containsExa(got) {
			t.Fatalf("exa not in the keyless list: %+v", got)
		}
	})

	t.Run("not listed when a key resolved", func(t *testing.T) {
		got := enabledButKeylessSearchProviders(WebSearchToolOptions{ExaEnabled: true, ExaAPIKey: "k"})
		if containsExa(got) {
			t.Fatalf("exa listed though a key resolved: %+v", got)
		}
	})

	t.Run("not listed when disabled", func(t *testing.T) {
		got := enabledButKeylessSearchProviders(WebSearchToolOptions{ExaEnabled: false})
		if containsExa(got) {
			t.Fatalf("exa listed though disabled: %+v", got)
		}
	})

	t.Run("six keyed providers when all enabled and keyless", func(t *testing.T) {
		opts := WebSearchToolOptions{
			PerplexityEnabled:  true,
			BraveEnabled:       true,
			TavilyEnabled:      true,
			GLMSearchEnabled:   true,
			BaiduSearchEnabled: true,
			ExaEnabled:         true,
		}
		got := enabledButKeylessSearchProviders(opts)
		names := make([]string, 0, len(got))
		for _, m := range got {
			names = append(names, m.name)
		}
		sort.Strings(names)
		want := []string{"baidu_search", "brave", "exa", "glm_search", "perplexity", "tavily"}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("keyless list = %v, want %v", names, want)
		}
	})
}
