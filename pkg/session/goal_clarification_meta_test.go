// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_clarification_meta_test.go pins the ADR-074 D4a pending-clarification
// record's session-meta plumbing (judgment-first spec US-3 S7/S10): the
// GoalClarificationJSON field must round-trip through SetMeta -> goal.json ->
// GetMeta (including a cold re-read with the cache dropped), and clear to
// empty like every other Goal* field.

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoalClarificationJSON_RoundTripAndClear(t *testing.T) {
	store, err := NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	sid := meta.ID

	record := `{"intent":"improve the docs","question":"Which docs?"}`
	if serr := store.SetMeta(sid, MetaPatch{GoalClarificationJSON: &record}); serr != nil {
		t.Fatal(serr)
	}

	// Warm read (cache).
	got, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got.GoalClarificationJSON != record {
		t.Fatalf("warm GoalClarificationJSON = %q, want %q", got.GoalClarificationJSON, record)
	}

	// The record must live in goal.json (the U5 split's goal group), not be
	// cache-only.
	goalPath := filepath.Join(store.baseDir, sid, "goal.json")
	raw, err := os.ReadFile(goalPath)
	if err != nil {
		t.Fatalf("goal.json must exist after a clarification write: %v", err)
	}
	if !strings.Contains(string(raw), "goal_clarification") {
		t.Fatalf("goal.json lacks goal_clarification key: %s", raw)
	}

	// Cold read: a fresh store over the same dir must compose the field back.
	cold, err := NewUnifiedStore(store.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	gotCold, err := cold.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if gotCold.GoalClarificationJSON != record {
		t.Fatalf("cold GoalClarificationJSON = %q, want %q", gotCold.GoalClarificationJSON, record)
	}

	// Clear via the empty-string convention.
	empty := ""
	if serr := store.SetMeta(sid, MetaPatch{GoalClarificationJSON: &empty}); serr != nil {
		t.Fatal(serr)
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalClarificationJSON != "" {
		t.Fatalf("GoalClarificationJSON after clear = %q, want empty", after.GoalClarificationJSON)
	}
}
