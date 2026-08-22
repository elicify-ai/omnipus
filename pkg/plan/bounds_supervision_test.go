// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"encoding/json"
	"errors"
	"testing"
)

// The two supervision fields on PlanBounds are per-plan OVERRIDES of the global
// config.PlanningConfig keys. These tests pin the two things a caller depends
// on: an out-of-range override is rejected at the store boundary (never
// persisted as a plan that can never make progress), and an in-range one
// survives the disk round-trip so the resolver actually sees it.

func boundsIntPtr(v int) *int { return &v }

func TestPlanBounds_SupervisionOverrides_PersistAndReload(t *testing.T) {
	s := newStore(t)

	p := mkPlan("Bounded Plan", "ws-1", "agent-a")
	p.Bounds = &PlanBounds{
		SupervisionTurnTimeoutSeconds: boundsIntPtr(120),
		SupervisionMaxAttempts:        boundsIntPtr(5),
	}
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Bounds == nil {
		t.Fatal("bounds did not survive the round-trip")
	}
	if got.Bounds.SupervisionTurnTimeoutSeconds == nil || *got.Bounds.SupervisionTurnTimeoutSeconds != 120 {
		t.Errorf("supervision_turn_timeout_seconds = %v, want 120", got.Bounds.SupervisionTurnTimeoutSeconds)
	}
	if got.Bounds.SupervisionMaxAttempts == nil || *got.Bounds.SupervisionMaxAttempts != 5 {
		t.Errorf("supervision_max_attempts = %v, want 5", got.Bounds.SupervisionMaxAttempts)
	}
}

// TestPlanBounds_SupervisionOverrides_JSONKeys pins the on-disk key names —
// they must match the global config.PlanningConfig keys an operator already
// knows, and an absent override must be absent from the JSON entirely (nil,
// not 0) so the resolver can tell "inherit" from "explicitly zero".
func TestPlanBounds_SupervisionOverrides_JSONKeys(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(&PlanBounds{SupervisionMaxAttempts: boundsIntPtr(2)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["supervision_turn_timeout_seconds"]; ok {
		t.Errorf("an unset override must be omitted, got %s", raw)
	}
	v, ok := decoded["supervision_max_attempts"]
	if !ok {
		t.Errorf("supervision_max_attempts key missing or wrong: %s", raw)
	} else if vFloat, ok := v.(float64); !ok || vFloat != 2 {
		t.Errorf("supervision_max_attempts key missing or wrong: %s", raw)
	}
}

func TestPlanBounds_SupervisionOverrides_RejectOutOfRange(t *testing.T) {
	tests := []struct {
		name   string
		bounds *PlanBounds
		want   string
	}{
		{
			"zero timeout",
			&PlanBounds{SupervisionTurnTimeoutSeconds: boundsIntPtr(0)},
			"supervision_turn_timeout_seconds",
		},
		{
			"negative timeout",
			&PlanBounds{SupervisionTurnTimeoutSeconds: boundsIntPtr(-1)},
			"supervision_turn_timeout_seconds",
		},
		{
			"zero attempts",
			&PlanBounds{SupervisionMaxAttempts: boundsIntPtr(0)},
			"supervision_max_attempts",
		},
		{
			"negative attempts",
			&PlanBounds{SupervisionMaxAttempts: boundsIntPtr(-3)},
			"supervision_max_attempts",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			p := mkPlan("Bad Bounds", "ws-1", "agent-a")
			p.Bounds = tc.bounds
			err := s.Create(p)
			if err == nil {
				t.Fatalf("expected %s to be rejected, plan was persisted", tc.want)
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("error must wrap ErrValidation (so REST maps it to 400), got %v", err)
			}
			rows, listErr := s.List(Filter{})
			if listErr != nil {
				t.Fatalf("List: %v", listErr)
			}
			if len(rows) != 0 {
				t.Errorf("a rejected plan must not be persisted, got %d rows", len(rows))
			}
		})
	}
}
