// workspace_seed_defaults_test.go — RED pack, session-core U5a condition 1:
// the config-file-only `workspace_seed_defaults.self_edge.exclude_agent_ids`
// surface.
//
// Expected values below derive from the SPEC, never from the code under test:
//   - DESIGN-RULING-delegation-20261009.md::The ruling points 3-4 — the
//     self-edge pre-seed moves to default config and the exclusion is
//     "expressed in config terms, not as an id check in Go".
//   - build/arch-design-delegation-evidence-20261009/seed-owner-decision.md
//     ::New operator config-file-only shape — the exact JSON key path and the
//     shipped ["judge","plansupervisor"] default, plus "Operator data wins,
//     including explicit []".
//   - docs/internal/specs/session-core-spec.md (architect c4b574b9c)
//     ::C-DELEGATE "Seed config data" / FR-014-015 / BDD-05.7.
//
// Current code (branch cut from 6ffb2eb20): no such key exists anywhere.
// DefaultConfig() does not emit it, and LoadConfig's lenient decode
// (no DisallowUnknownFields) silently drops it. Every test below fails on the
// pre-change code for exactly that reason.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// workspaceSeedDefaultsKeyPath is the exact operator-facing key the
// seed-owner-decision names. Written as a single literal so a GREEN that
// guesses a different key/shape fails loudly here.
const workspaceSeedDefaultsKeyPath = "workspace_seed_defaults.self_edge.exclude_agent_ids"

// nestedConfigValue walks a decoded config map along path, reporting whether
// every segment was present.
func nestedConfigValue(m map[string]any, path ...string) (any, bool) {
	var cur any = m
	for _, seg := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// configStringIDs coerces a decoded JSON array into its []string form, failing
// the test on any non-string member (so a bool/number can never pass as an id).
func configStringIDs(t *testing.T, raw any, key string) []string {
	t.Helper()
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s must decode to a JSON array, got %T (%v)", key, raw, raw)
	}
	ids := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s entries must be agent-id strings, got %T (%v)", key, v, v)
		}
		ids = append(ids, s)
	}
	return ids
}

// TestWorkspaceSeedDefaults_DefaultConfigShipsJudgeAndPlansupervisor pins the
// shipped default: a fresh install's config carries the exclusion list set to
// exactly the two hidden type:system seed records (judge, plansupervisor).
func TestWorkspaceSeedDefaults_DefaultConfigShipsJudgeAndPlansupervisor(t *testing.T) {
	raw, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatalf("marshal DefaultConfig: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal DefaultConfig map: %v", err)
	}
	got, ok := nestedConfigValue(m, "workspace_seed_defaults", "self_edge", "exclude_agent_ids")
	if !ok {
		t.Fatalf("BLOCKED: %s is not materialized in DefaultConfig() — required by the U5a "+
			"condition-1 shape (seed-owner-decision ::New operator config-file-only shape)",
			workspaceSeedDefaultsKeyPath)
	}
	ids := configStringIDs(t, got, workspaceSeedDefaultsKeyPath)
	// Shipped default = the two hidden system seed records only. Planner is a
	// native worker and is deliberately NOT excluded (seed-owner-decision).
	want := map[string]bool{"judge": true, "plansupervisor": true}
	if len(ids) != len(want) {
		t.Fatalf("%s shipped default = %v, want exactly [judge plansupervisor]",
			workspaceSeedDefaultsKeyPath, ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("%s shipped default = %v, contains unexpected id %q (want exactly judge+plansupervisor)",
				workspaceSeedDefaultsKeyPath, ids, id)
		}
	}
}

// TestWorkspaceSeedDefaults_LoadedAsKnownTypedField pins that the key is a
// KNOWN, TYPED config field the seed code can read — not merely an unknown-key
// passthrough.
//
// WHY THIS SHAPE (a real false-green this pack found): Config already carries
// Config.UnknownFields (FR-004 round-trip safety) and Config.MarshalJSON
// re-emits every unrecognized key verbatim. So a naive "write the key, load,
// marshal, assert the key survived" test PASSES on the pre-change code — the
// value is round-tripped as an UNKNOWN field with no typed field and no
// consumer. That round-trip is exactly the U5a-shape false green to avoid, so
// this test instead asserts the key was CONSUMED as a known field: it must NOT
// be parked in UnknownFields. On the pre-change code it is (there is no typed
// field), so this fails for the right reason.
func TestWorkspaceSeedDefaults_LoadedAsKnownTypedField(t *testing.T) {
	dir := t.TempDir()
	cfgMap := map[string]any{
		"version": CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": dir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 5000},
		"providers": []any{},
		"workspace_seed_defaults": map[string]any{
			"self_edge": map[string]any{"exclude_agent_ids": []any{}},
		},
	}
	body, err := json.Marshal(cfgMap)
	if err != nil {
		t.Fatalf("marshal config.json body: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if _, passthrough := cfg.UnknownFields["workspace_seed_defaults"]; passthrough {
		t.Fatalf("BLOCKED: %s was loaded as an UNKNOWN-FIELD passthrough (Config.UnknownFields) — "+
			"it must be a KNOWN typed config field the seed code reads (seed-owner-decision "+
			"::New operator config-file-only shape). A round-trip assertion alone would be a FALSE "+
			"GREEN here, because UnknownFields already re-emits any unknown key.", workspaceSeedDefaultsKeyPath)
	}
}
