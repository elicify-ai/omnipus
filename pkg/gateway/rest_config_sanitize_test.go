// Omnipus — sanitizeConfigForWire unit tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import "testing"

// TestSanitizeConfigForWire pins the shared wire-sanitizer getConfig (and
// any future config-serving endpoint) relies on: every key listed in
// wireExcludedConfigFields is removed, everything else is untouched, and
// the current exclusion list still covers skills_migrations (the ADR-074 D4
// internal-only marker that must never cross the wire).
func TestSanitizeConfigForWire(t *testing.T) {
	m := map[string]any{
		"gateway":           map[string]any{"port": float64(5000)},
		"skills_migrations": []any{"define-skill-allowlist"},
	}
	for _, k := range wireExcludedConfigFields {
		m[k] = "internal"
	}

	sanitizeConfigForWire(m)

	for _, k := range wireExcludedConfigFields {
		if _, present := m[k]; present {
			t.Fatalf("wire-excluded key %q survived sanitizeConfigForWire", k)
		}
	}
	if _, present := m["skills_migrations"]; present {
		t.Fatal("skills_migrations must be in the exclusion list and stripped from the wire")
	}
	if _, present := m["gateway"]; !present {
		t.Fatal("sanitizeConfigForWire must not touch keys outside wireExcludedConfigFields")
	}
}
