// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// goal_legacy_migration_test.go — the ADR-091 goal -> objective rename is a
// wire/tool-surface rename ONLY. It must NOT silently drop data already on
// disk: every plan written before this rename has `"goal": "..."` and no
// `"objective"` key at $OMNIPUS_HOME/plans/<id>.json (confirmed against the
// founder's own installation). Without a read-time migration, every existing
// plan loads with an EMPTY Objective and the plan judge is handed a blank
// "Objective:" line — silent loss of the plan's stated aim.
//
// Plan.UnmarshalJSON (plan.go) is the fix: a legacy `goal` key is read into
// Objective only when `objective` itself is absent, so a file already
// carrying the new key is never overridden by a stale legacy one. This is
// purely a DATA migration for pre-existing on-disk files — it is NOT an
// agent-facing alias and does not contradict "delete old code, no
// deprecation period": create_plan's tool argument, the REST wire field, and
// the schema all still reject `goal` outright with no fallback (see
// plan_test.go/plan_correct_test.go's forbidden-field guards and
// PlanCreateTool.Execute's explicit rejection). Do not "clean up" this
// migration — it is what keeps every plan written by a pre-rename build
// readable.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeRawPlanFile writes literal JSON bytes at the store's expected path for
// id, bypassing Store.Create/Update entirely — this is what an on-disk file
// from BEFORE the rename actually looks like.
func writeRawPlanFile(t *testing.T, s *Store, id, rawJSON string) {
	t.Helper()
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), id+".json"), []byte(rawJSON), 0o600); err != nil {
		t.Fatalf("write raw plan file: %v", err)
	}
}

// TestPlan_Load_LegacyGoalKeyMigratesToObjective proves a pre-rename plan
// file (goal populated, no objective key at all) loads with Objective set
// from the legacy goal value — the founder's own on-disk plans are exactly
// this shape.
func TestPlan_Load_LegacyGoalKeyMigratesToObjective(t *testing.T) {
	s := newStore(t)
	writeRawPlanFile(t, s, "legacy-1", `{
		"id": "legacy-1",
		"workspace_id": "ws-1",
		"title": "Pre-rename plan",
		"goal": "Ship the v1.0 release with all P0 issues closed and CI green.",
		"owner_agent_id": "jim",
		"owner": "alice",
		"created_by": "alice",
		"state": "draft"
	}`)

	p, err := s.Get("legacy-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Objective != "Ship the v1.0 release with all P0 issues closed and CI green." {
		t.Errorf("Objective = %q, want the legacy goal value migrated across", p.Objective)
	}
}

// TestPlan_Load_ObjectiveKeyLoadsNormally proves a plan file already written
// in the new shape (objective, no goal) loads unaffected by the migration.
func TestPlan_Load_ObjectiveKeyLoadsNormally(t *testing.T) {
	s := newStore(t)
	writeRawPlanFile(t, s, "new-1", `{
		"id": "new-1",
		"workspace_id": "ws-1",
		"title": "Post-rename plan",
		"objective": "Ship v2.0.",
		"owner_agent_id": "jim",
		"owner": "alice",
		"created_by": "alice",
		"state": "draft"
	}`)

	p, err := s.Get("new-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Objective != "Ship v2.0." {
		t.Errorf("Objective = %q, want %q", p.Objective, "Ship v2.0.")
	}
}

// TestPlan_Load_BothKeysPrefersObjective proves that when a file somehow
// carries both keys (e.g. a hand-edited or transitional file), the new
// `objective` key wins — the legacy key is a fallback, never an override.
func TestPlan_Load_BothKeysPrefersObjective(t *testing.T) {
	s := newStore(t)
	writeRawPlanFile(t, s, "both-1", `{
		"id": "both-1",
		"workspace_id": "ws-1",
		"title": "Both keys present",
		"goal": "stale legacy value",
		"objective": "current value",
		"owner_agent_id": "jim",
		"owner": "alice",
		"created_by": "alice",
		"state": "draft"
	}`)

	p, err := s.Get("both-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Objective != "current value" {
		t.Errorf("Objective = %q, want the objective key to win over the legacy goal key", p.Objective)
	}
}

// TestPlan_Load_LegacyGoalDrainsToObjectiveOnNextSave proves the legacy key
// is not just read-compatible but actively drains away: once a migrated plan
// is written back through the store (any Update), the persisted JSON carries
// `objective`, not `goal` — so a plan touched even once under the new build
// stops depending on this migration at all.
func TestPlan_Load_LegacyGoalDrainsToObjectiveOnNextSave(t *testing.T) {
	s := newStore(t)
	writeRawPlanFile(t, s, "drain-1", `{
		"id": "drain-1",
		"workspace_id": "ws-1",
		"title": "Draining plan",
		"goal": "Migrate me",
		"owner_agent_id": "jim",
		"owner": "alice",
		"created_by": "alice",
		"state": "draft"
	}`)

	// Any Update writes the plan back through Store.write, which marshals
	// the current in-memory Plan (Objective, never Goal — that field no
	// longer exists on the struct).
	updated, err := s.Update("drain-1", Patch{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Objective != "Migrate me" {
		t.Fatalf("Objective after Update = %q, want the migrated value preserved", updated.Objective)
	}

	raw, err := os.ReadFile(filepath.Join(s.Dir(), "drain-1.json"))
	if err != nil {
		t.Fatalf("read back raw file: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal raw file: %v", err)
	}
	if _, hasGoal := onDisk["goal"]; hasGoal {
		t.Errorf("rewritten file still carries the legacy %q key: %s", "goal", raw)
	}
	if got, _ := onDisk["objective"].(string); got != "Migrate me" {
		t.Errorf("rewritten file's %q key = %q, want %q", "objective", got, "Migrate me")
	}
}
