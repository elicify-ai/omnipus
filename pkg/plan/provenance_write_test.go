// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"reflect"
	"testing"
)

// I-2 / S-111 (ruling-check-delta "S-111 writers"): a plan stores the
// provenance its creator gave it; an agent instruction change joins the
// writer's origins in the same write; a write with no Patch.Provenance (a
// person's edit, a state change) leaves it unchanged.
func TestPlanProvenance_CreateStoresAndUpdateJoins(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Provenance Plan", "ws-1", "agent-a")
	p.Provenance = []string{"web"}
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := []string{"web"}; !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("created provenance = %v, want %v", got.Provenance, want)
	}

	// A person's edit of an instruction field carries no provenance: unchanged.
	title := "Edited by a person"
	edited, err := s.Update(p.ID, Patch{Title: &title})
	if err != nil {
		t.Fatalf("person Update: %v", err)
	}
	if want := []string{"web"}; !reflect.DeepEqual(edited.Provenance, want) {
		t.Fatalf("after a person's edit provenance = %v, want unchanged %v", edited.Provenance, want)
	}

	// An agent instruction change joins its turn's origins.
	objective := "Changed by an agent"
	agentTurn := []string{"connector:a"}
	joined, err := s.Update(p.ID, Patch{Objective: &objective, Provenance: &agentTurn})
	if err != nil {
		t.Fatalf("agent Update: %v", err)
	}
	want := []string{"connector:a", "web"}
	if !reflect.DeepEqual(joined.Provenance, want) {
		t.Fatalf("after an agent change provenance = %v, want the join %v", joined.Provenance, want)
	}
	reloaded, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get after join: %v", err)
	}
	if !reflect.DeepEqual(reloaded.Provenance, want) {
		t.Fatalf("persisted provenance = %v, want %v", reloaded.Provenance, want)
	}

	// An unresolved writer (empty origins) makes the plan unresolved.
	unresolved := []string{}
	failed, err := s.Update(p.ID, Patch{Objective: &objective, Provenance: &unresolved})
	if err != nil {
		t.Fatalf("unresolved Update: %v", err)
	}
	if len(failed.Provenance) != 0 {
		t.Fatalf("an unresolved writer must leave the plan unresolved, got %v", failed.Provenance)
	}
}
