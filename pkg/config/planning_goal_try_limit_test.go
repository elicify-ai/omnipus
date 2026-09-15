// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"testing"
)

// TestEffectiveTaskMaxAttempts_SeparateFromGoalTryLimit pins the founder
// decision of 2026-09-14 (confirmed the same day): goal tries and task attempts
// are TWO separate limits. The goal try limit (Settings -> Performance
// goal_max_rounds) bounds how many tries a goal gets within one run; the task
// attempt limit bounds how many fresh runs a task gets — the task's own
// max_attempts, else planning.task_max_attempts, else 3. The expected numbers
// come from that decision, not from the implementation.
func TestEffectiveTaskMaxAttempts_SeparateFromGoalTryLimit(t *testing.T) {
	two := 2
	zero := 0
	cases := []struct {
		name     string
		cfg      PlanningConfig
		override *int
		want     int
	}{
		{"nothing set: the default of 3", PlanningConfig{}, nil, 3},
		{"the goal try limit does not move the task attempt limit", PlanningConfig{GoalMaxRounds: 5}, nil, 3},
		{"the global task attempt limit applies", PlanningConfig{TaskMaxAttempts: 4, GoalMaxRounds: 5}, nil, 4},
		{"a per-task max_attempts wins", PlanningConfig{TaskMaxAttempts: 4}, &two, 2},
		{"a per-task max_attempts below 1 is not an override", PlanningConfig{TaskMaxAttempts: 4}, &zero, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveTaskMaxAttempts(tc.override); got != tc.want {
				t.Fatalf("EffectiveTaskMaxAttempts(override=%v) with task_max_attempts=%d goal_max_rounds=%d = %d, want %d",
					tc.override, tc.cfg.TaskMaxAttempts, tc.cfg.GoalMaxRounds, got, tc.want)
			}
		})
	}
	if got := (PlanningConfig{TaskMaxAttempts: 4}).EffectiveGoalMaxRounds(); got != 20 {
		t.Fatalf("the task attempt limit must not move the goal try limit: EffectiveGoalMaxRounds = %d, want 20", got)
	}
}

// TestPlanningConfig_TaskMaxAttemptsSeededAndReadFromConfig proves a fresh
// install writes both limits with their own defaults (3 and 20), and that a
// config.json naming task_max_attempts is honoured again (commit f78d77de3 had
// retired the key).
func TestPlanningConfig_TaskMaxAttemptsSeededAndReadFromConfig(t *testing.T) {
	var p PlanningConfig
	if err := json.Unmarshal([]byte(`{"task_max_attempts": 4, "goal_max_rounds": 5}`), &p); err != nil {
		t.Fatalf("unmarshal planning config: %v", err)
	}
	if got := p.EffectiveTaskMaxAttempts(nil); got != 4 {
		t.Fatalf("task attempt limit = %d, want 4 from task_max_attempts", got)
	}
	if got := p.EffectiveGoalMaxRounds(); got != 5 {
		t.Fatalf("goal try limit = %d, want 5 from goal_max_rounds", got)
	}

	raw, err := json.Marshal(DefaultConfig().Planning)
	if err != nil {
		t.Fatalf("marshal default planning config: %v", err)
	}
	var seeded map[string]any
	if err := json.Unmarshal(raw, &seeded); err != nil {
		t.Fatalf("unmarshal default planning config: %v", err)
	}
	if got, ok := seeded["task_max_attempts"].(float64); !ok || got != 3 {
		t.Fatalf("a fresh install seeds task_max_attempts=%v, want 3", seeded["task_max_attempts"])
	}
	if got, ok := seeded["goal_max_rounds"].(float64); !ok || got != 20 {
		t.Fatalf("a fresh install seeds goal_max_rounds=%v, want 20", seeded["goal_max_rounds"])
	}
}
