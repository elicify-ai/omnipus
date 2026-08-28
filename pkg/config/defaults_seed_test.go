// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestDefaultsSeed_NoRemovedProvider (ADR-068 spec TDD row 34, FR-040) pins
// the fresh-install seed after the ADR-068 §2.4 provider deletions:
//
//   - no model template names a removed provider (the OAuth-only Google
//     Cloud Code Assist provider, the Claude CLI executor);
//   - no template carries an auth-method override — every seeded template is
//     an API-key template, so the struct either has no such field or it is
//     the zero value (checked by reflection so the assertion survives the
//     field's deletion in T068-03);
//   - agents.defaults' default model stays at its zero value: onboarding's
//     explicit pick is the only writer on a fresh install.
//
// The removed ids are spelled by fragment so this test is not itself a trace
// (scripts/check-no-removed-providers.sh scans pkg/ for the names).
func TestDefaultsSeed_NoRemovedProvider(t *testing.T) {
	cfg := DefaultConfig()

	removedPrefixes := []string{"anti" + "gravity/", "claude" + "-cli/", "claudecli/"}
	for _, tpl := range cfg.Providers {
		for _, p := range removedPrefixes {
			if strings.HasPrefix(strings.ToLower(tpl.Model), p) {
				t.Errorf("seed template %q names a removed provider: model=%q", tpl.Name, tpl.Model)
			}
		}
		if f := reflect.ValueOf(tpl).Elem().FieldByName("AuthMethod"); f.IsValid() && !f.IsZero() {
			t.Errorf("seed template %q sets an auth-method override %v; fresh-install templates are API-key only", tpl.Name, f.Interface())
		}
	}

	if !cfg.Agents.Defaults.DefaultModel.IsZero() {
		t.Errorf("agents.defaults.default_model = %+v, want the zero pair (FR-040)", cfg.Agents.Defaults.DefaultModel)
	}
}
