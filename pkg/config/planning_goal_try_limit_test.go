// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"testing"
)

// TestEffectiveTaskMaxAttempts_ResolvesFromGoalTryLimit pins the founder
// decision of 2026-09-14: Settings -> Performance "goal_max_rounds" is the ONE
// setting that bounds how many tries a goal gets, for a goal set in chat AND a
// goal on a task (ADR-086 GOAL-FR-024 "one budget for both owner kinds,
// defaulting to 20"; D-D/D-E). The task attempt ceiling must therefore resolve
// from that value. Expected numbers below come from that decision and the
// ratified R-03 text ("Task.MaxAttempts stays as the per-task override"), not
// from reading the implementation.
func TestEffectiveTaskMaxAttempts_ResolvesFromGoalTryLimit(t *testing.T) {
	two := 2
	zero := 0
	cases := []struct {
		name     string
		cfg      PlanningConfig
		override *int
		running  int
		want     int
	}{
		{"no override, run not started: the live goal try limit", PlanningConfig{GoalMaxRounds: 5}, nil, 0, 5},
		{"unset goal try limit: the shipped default of 20", PlanningConfig{}, nil, 0, 20},
		{"started run keeps the limit snapshotted at its start", PlanningConfig{GoalMaxRounds: 20}, nil, 5, 5},
		{"per-task max_attempts beats the snapshot and the global", PlanningConfig{GoalMaxRounds: 5}, &two, 5, 2},
		{"per-task max_attempts below 1 is not an override", PlanningConfig{GoalMaxRounds: 5}, &zero, 0, 5},
		{"a non-positive snapshot is not a limit", PlanningConfig{GoalMaxRounds: 7}, nil, -1, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveTaskMaxAttempts(tc.override, tc.running); got != tc.want {
				t.Fatalf("EffectiveTaskMaxAttempts(override=%v, running=%d) with goal_max_rounds=%d = %d, want %d",
					tc.override, tc.running, tc.cfg.GoalMaxRounds, got, tc.want)
			}
		})
	}
}

// TestPlanningConfig_RetiredTaskMaxAttemptsKeyBoundsNothing proves the retired
// `planning.task_max_attempts` key can no longer override the Settings value:
// every install created before this change carries `"task_max_attempts": 20`
// in its config.json (DefaultConfig used to seed it), and before the fix that
// seeded key — not the Settings value — bounded every task. A config naming 3
// for the retired key and 5 for the goal try limit must bound tasks at 5.
// It also proves a fresh install no longer writes the retired key at all.
func TestPlanningConfig_RetiredTaskMaxAttemptsKeyBoundsNothing(t *testing.T) {
	var p PlanningConfig
	if err := json.Unmarshal([]byte(`{"task_max_attempts": 3, "goal_max_rounds": 5}`), &p); err != nil {
		t.Fatalf("unmarshal planning config: %v", err)
	}
	if got := p.EffectiveTaskMaxAttempts(nil, 0); got != 5 {
		t.Fatalf("task attempt ceiling = %d, want 5 (the goal try limit) — the retired task_max_attempts key must not bound tasks", got)
	}

	raw, err := json.Marshal(DefaultConfig().Planning)
	if err != nil {
		t.Fatalf("marshal default planning config: %v", err)
	}
	var seeded map[string]any
	if err := json.Unmarshal(raw, &seeded); err != nil {
		t.Fatalf("unmarshal default planning config: %v", err)
	}
	if v, ok := seeded["task_max_attempts"]; ok {
		t.Fatalf("a fresh install seeds task_max_attempts=%v; the retired key must not be written", v)
	}
	if got, ok := seeded["goal_max_rounds"].(float64); !ok || got != 20 {
		t.Fatalf("a fresh install seeds goal_max_rounds=%v, want 20", seeded["goal_max_rounds"])
	}
}
