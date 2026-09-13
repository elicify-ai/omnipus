// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestPlanningConfig_DefaultsAndValidation exercises validateBootConfig's
// ADR-049 D7 bounds block (spec Part A §G): each field, when zero, gets its
// documented default; each field, when non-zero, must be >=1;
// CheckTimeoutSeconds additionally has an upper bound of 3600.
func TestPlanningConfig_DefaultsAndValidation(t *testing.T) {
	t.Run("all-zero applies every documented default", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning = PlanningConfig{}
		if err := validateBootConfig(cfg); err != nil {
			t.Fatalf("expected nil for all-zero Planning config, got %v", err)
		}
		want := PlanningConfig{
			TaskMaxAttempts:         DefaultTaskMaxAttempts,
			GoalMaxRounds:           DefaultGoalMaxRounds,
			PlanJudgeMaxRounds:      DefaultPlanJudgeMaxRounds,
			LoopMaxRuns:             DefaultLoopMaxRuns,
			IdleExpiryDays:          DefaultIdleExpiryDays,
			GlobalActiveLoopCap:     DefaultGlobalActiveLoopCap,
			CheckTimeoutSeconds:     DefaultCheckTimeoutSeconds,
			VerifierWindowTokens:    DefaultVerifierWindowTokens,
			GoalCompileWindowTokens: DefaultGoalCompileWindowTokens,
		}
		if cfg.Planning != want {
			t.Fatalf("defaults not applied: got %+v, want %+v", cfg.Planning, want)
		}
	})

	t.Run("negative fields rejected", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*PlanningConfig)
		}{
			{"TaskMaxAttempts", func(p *PlanningConfig) { p.TaskMaxAttempts = -1 }},
			{"GoalMaxRounds", func(p *PlanningConfig) { p.GoalMaxRounds = -1 }},
			{"PlanJudgeMaxRounds", func(p *PlanningConfig) { p.PlanJudgeMaxRounds = -1 }},
			{"LoopMaxRuns", func(p *PlanningConfig) { p.LoopMaxRuns = -1 }},
			{"IdleExpiryDays", func(p *PlanningConfig) { p.IdleExpiryDays = -1 }},
			{"GlobalActiveLoopCap", func(p *PlanningConfig) { p.GlobalActiveLoopCap = -1 }},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				cfg := minimalValidConfig()
				cfg.Planning = PlanningConfig{
					TaskMaxAttempts:      DefaultTaskMaxAttempts,
					GoalMaxRounds:        DefaultGoalMaxRounds,
					PlanJudgeMaxRounds:   DefaultPlanJudgeMaxRounds,
					LoopMaxRuns:          DefaultLoopMaxRuns,
					IdleExpiryDays:       DefaultIdleExpiryDays,
					GlobalActiveLoopCap:  DefaultGlobalActiveLoopCap,
					CheckTimeoutSeconds:  DefaultCheckTimeoutSeconds,
					VerifierWindowTokens: DefaultVerifierWindowTokens,
				}
				c.mutate(&cfg.Planning)
				if err := validateBootConfig(cfg); err == nil {
					t.Fatalf("expected error for negative %s", c.name)
				}
			})
		}
	})

	t.Run("CheckTimeoutSeconds zero applies default", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning.CheckTimeoutSeconds = 0
		if err := validateBootConfig(cfg); err != nil {
			t.Fatalf("expected nil for CheckTimeoutSeconds=0, got %v", err)
		}
		if cfg.Planning.CheckTimeoutSeconds != DefaultCheckTimeoutSeconds {
			t.Fatalf("expected default %d, got %d", DefaultCheckTimeoutSeconds, cfg.Planning.CheckTimeoutSeconds)
		}
	})

	t.Run("CheckTimeoutSeconds above 3600 rejected", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning.CheckTimeoutSeconds = 3601
		if err := validateBootConfig(cfg); err == nil {
			t.Fatal("expected error for CheckTimeoutSeconds=3601")
		}
	})

	t.Run("CheckTimeoutSeconds at upper bound accepted", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning.CheckTimeoutSeconds = 3600
		if err := validateBootConfig(cfg); err != nil {
			t.Fatalf("expected nil for CheckTimeoutSeconds=3600, got %v", err)
		}
	})

	// ADR-052 FR-032 — VerifierWindowTokens follows the same zero-value
	// backfill / >=1 validation pattern as the other Planning fields.
	t.Run("VerifierWindowTokens zero applies default", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning.VerifierWindowTokens = 0
		if err := validateBootConfig(cfg); err != nil {
			t.Fatalf("expected nil for VerifierWindowTokens=0, got %v", err)
		}
		if cfg.Planning.VerifierWindowTokens != DefaultVerifierWindowTokens {
			t.Fatalf("expected default %d, got %d", DefaultVerifierWindowTokens, cfg.Planning.VerifierWindowTokens)
		}
	})

	t.Run("VerifierWindowTokens negative rejected", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning = PlanningConfig{
			TaskMaxAttempts:      DefaultTaskMaxAttempts,
			GoalMaxRounds:        DefaultGoalMaxRounds,
			PlanJudgeMaxRounds:   DefaultPlanJudgeMaxRounds,
			LoopMaxRuns:          DefaultLoopMaxRuns,
			IdleExpiryDays:       DefaultIdleExpiryDays,
			GlobalActiveLoopCap:  DefaultGlobalActiveLoopCap,
			CheckTimeoutSeconds:  DefaultCheckTimeoutSeconds,
			VerifierWindowTokens: -1,
		}
		if err := validateBootConfig(cfg); err == nil {
			t.Fatal("expected error for negative VerifierWindowTokens")
		}
	})

	// ADR-079 D1 — GoalCompileWindowTokens follows the same zero-value
	// backfill / >=1 validation pattern as VerifierWindowTokens above.
	t.Run("GoalCompileWindowTokens zero applies default", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning.GoalCompileWindowTokens = 0
		if err := validateBootConfig(cfg); err != nil {
			t.Fatalf("expected nil for GoalCompileWindowTokens=0, got %v", err)
		}
		if cfg.Planning.GoalCompileWindowTokens != DefaultGoalCompileWindowTokens {
			t.Fatalf("expected default %d, got %d", DefaultGoalCompileWindowTokens, cfg.Planning.GoalCompileWindowTokens)
		}
	})

	t.Run("GoalCompileWindowTokens negative rejected", func(t *testing.T) {
		cfg := minimalValidConfig()
		cfg.Planning = PlanningConfig{
			TaskMaxAttempts:         DefaultTaskMaxAttempts,
			GoalMaxRounds:           DefaultGoalMaxRounds,
			PlanJudgeMaxRounds:      DefaultPlanJudgeMaxRounds,
			LoopMaxRuns:             DefaultLoopMaxRuns,
			IdleExpiryDays:          DefaultIdleExpiryDays,
			GlobalActiveLoopCap:     DefaultGlobalActiveLoopCap,
			CheckTimeoutSeconds:     DefaultCheckTimeoutSeconds,
			VerifierWindowTokens:    DefaultVerifierWindowTokens,
			GoalCompileWindowTokens: -1,
		}
		if err := validateBootConfig(cfg); err == nil {
			t.Fatal("expected error for negative GoalCompileWindowTokens")
		}
	})

	t.Run("DefaultConfig is already boot-valid", func(t *testing.T) {
		cfg := DefaultConfig()
		if err := validateBootConfig(cfg); err != nil {
			t.Fatalf("DefaultConfig() must pass validateBootConfig, got %v", err)
		}
		want := PlanningConfig{
			TaskMaxAttempts:         DefaultTaskMaxAttempts,
			GoalMaxRounds:           DefaultGoalMaxRounds,
			PlanJudgeMaxRounds:      DefaultPlanJudgeMaxRounds,
			LoopMaxRuns:             DefaultLoopMaxRuns,
			IdleExpiryDays:          DefaultIdleExpiryDays,
			GlobalActiveLoopCap:     DefaultGlobalActiveLoopCap,
			CheckTimeoutSeconds:     DefaultCheckTimeoutSeconds,
			VerifierWindowTokens:    DefaultVerifierWindowTokens,
			GoalCompileWindowTokens: DefaultGoalCompileWindowTokens,
		}
		if cfg.Planning != want {
			t.Fatalf("DefaultConfig().Planning = %+v, want %+v", cfg.Planning, want)
		}
	})
}

// TestBounds_PerEntityOverridesGlobal proves the FR-9 resolution order: a
// non-nil, valid per-entity override (Plan.Bounds / Task.MaxAttempts) always
// wins over the global PlanningConfig value, and a nil/invalid override falls
// back to the global config (or its documented default when the global
// config itself is zero-valued).
func TestBounds_PerEntityOverridesGlobal(t *testing.T) {
	global := PlanningConfig{
		TaskMaxAttempts:    3,
		PlanJudgeMaxRounds: 20,
		IdleExpiryDays:     7,
	}

	t.Run("TaskMaxAttempts override wins", func(t *testing.T) {
		override := 10
		if got := global.EffectiveTaskMaxAttempts(&override); got != 10 {
			t.Fatalf("EffectiveTaskMaxAttempts(override=10) = %d, want 10", got)
		}
	})
	t.Run("TaskMaxAttempts nil falls back to global", func(t *testing.T) {
		if got := global.EffectiveTaskMaxAttempts(nil); got != 3 {
			t.Fatalf("EffectiveTaskMaxAttempts(nil) = %d, want global 3", got)
		}
	})
	t.Run("TaskMaxAttempts invalid (<1) override falls back to global", func(t *testing.T) {
		override := 0
		if got := global.EffectiveTaskMaxAttempts(&override); got != 3 {
			t.Fatalf("EffectiveTaskMaxAttempts(override=0) = %d, want global 3", got)
		}
	})

	t.Run("PlanJudgeMaxRounds override wins", func(t *testing.T) {
		override := 5
		if got := global.EffectivePlanJudgeMaxRounds(&override); got != 5 {
			t.Fatalf("EffectivePlanJudgeMaxRounds(override=5) = %d, want 5", got)
		}
	})
	t.Run("PlanJudgeMaxRounds nil falls back to global", func(t *testing.T) {
		if got := global.EffectivePlanJudgeMaxRounds(nil); got != 20 {
			t.Fatalf("EffectivePlanJudgeMaxRounds(nil) = %d, want global 20", got)
		}
	})

	t.Run("IdleExpiryDays override wins", func(t *testing.T) {
		override := 14
		if got := global.EffectiveIdleExpiryDays(&override); got != 14 {
			t.Fatalf("EffectiveIdleExpiryDays(override=14) = %d, want 14", got)
		}
	})
	t.Run("IdleExpiryDays nil falls back to global", func(t *testing.T) {
		if got := global.EffectiveIdleExpiryDays(nil); got != 7 {
			t.Fatalf("EffectiveIdleExpiryDays(nil) = %d, want global 7", got)
		}
	})

	t.Run("zero-value global config falls back to package defaults", func(t *testing.T) {
		var zero PlanningConfig
		if got := zero.EffectiveTaskMaxAttempts(nil); got != DefaultTaskMaxAttempts {
			t.Fatalf("EffectiveTaskMaxAttempts on zero config = %d, want default %d", got, DefaultTaskMaxAttempts)
		}
		if got := zero.EffectivePlanJudgeMaxRounds(nil); got != DefaultPlanJudgeMaxRounds {
			t.Fatalf("EffectivePlanJudgeMaxRounds on zero config = %d, want default %d", got, DefaultPlanJudgeMaxRounds)
		}
		if got := zero.EffectiveIdleExpiryDays(nil); got != DefaultIdleExpiryDays {
			t.Fatalf("EffectiveIdleExpiryDays on zero config = %d, want default %d", got, DefaultIdleExpiryDays)
		}
		if got := zero.EffectiveVerifierWindowTokens(); got != DefaultVerifierWindowTokens {
			t.Fatalf("EffectiveVerifierWindowTokens on zero config = %d, want default %d", got, DefaultVerifierWindowTokens)
		}
		if got := zero.EffectiveGoalCompileWindowTokens(); got != DefaultGoalCompileWindowTokens {
			t.Fatalf("EffectiveGoalCompileWindowTokens on zero config = %d, want default %d", got, DefaultGoalCompileWindowTokens)
		}
	})

	t.Run("VerifierWindowTokens set value wins over default", func(t *testing.T) {
		g := PlanningConfig{VerifierWindowTokens: 5000}
		if got := g.EffectiveVerifierWindowTokens(); got != 5000 {
			t.Fatalf("EffectiveVerifierWindowTokens() = %d, want 5000", got)
		}
	})

	t.Run("GoalCompileWindowTokens set value wins over default", func(t *testing.T) {
		g := PlanningConfig{GoalCompileWindowTokens: 8000}
		if got := g.EffectiveGoalCompileWindowTokens(); got != 8000 {
			t.Fatalf("EffectiveGoalCompileWindowTokens() = %d, want 8000", got)
		}
	})
}

// TestGoalDefaultBudgetIsTwentyForBothOwners is GOAL-FR-024's oracle (goal
// spec test matrix row 24, S-33): "There MUST be one budget for both owner
// kinds, defaulting to 20." Operator decisions D-D/D-E (2026-09-11) retired
// the per-goal override GOAL-FR-025 originally asked for
// (EffectiveGoalMaxRoundsWithOverride is NEVER created — NQ-2/R-21
// amended) — EffectiveGoalMaxRounds keeps the exact signature it has today:
// zero arguments, no owner-kind parameter of any kind. That absence of an
// owner-kind parameter IS the "one budget for both owner kinds" guarantee:
// there is no code path by which a task-owned goal's session and a
// session-owned (chat) goal's session could ever resolve two different
// numbers from the same PlanningConfig — both read the identical global
// value the operator sets once, under Settings -> Performance
// (GOAL-FR-045).
func TestGoalDefaultBudgetIsTwentyForBothOwners(t *testing.T) {
	t.Run("unconfigured: both owner kinds resolve the documented default of 20", func(t *testing.T) {
		if DefaultGoalMaxRounds != 20 {
			t.Fatalf("DefaultGoalMaxRounds = %d, want 20 (GOAL-FR-024)", DefaultGoalMaxRounds)
		}
		var unconfigured PlanningConfig
		// "Both owner kinds" is not two different call sites with two
		// different arguments — it is the SAME zero-argument call, made
		// once for a chat-owned goal's resolution and once for a
		// task-owned goal's, from identical global config. There is no
		// owner-kind input this function could even branch on.
		chatGoalBudget := unconfigured.EffectiveGoalMaxRounds()
		taskGoalBudget := unconfigured.EffectiveGoalMaxRounds()
		if chatGoalBudget != 20 {
			t.Fatalf("EffectiveGoalMaxRounds() for a chat-owned goal = %d, want default 20", chatGoalBudget)
		}
		if taskGoalBudget != 20 {
			t.Fatalf("EffectiveGoalMaxRounds() for a task-owned goal = %d, want default 20", taskGoalBudget)
		}
		if chatGoalBudget != taskGoalBudget {
			t.Fatalf("chat-owned budget (%d) != task-owned budget (%d); GOAL-FR-024 requires ONE budget for both owner kinds",
				chatGoalBudget, taskGoalBudget)
		}
	})

	t.Run("operator-configured: both owner kinds see the same changed value identically", func(t *testing.T) {
		// D-D: "GOALS MUST BEHAVE IDENTICALLY for tasks and chat. Any
		// budget surface that exists for one MUST exist for the other."
		// An operator raising the one global setting (e.g. via PUT
		// /api/v1/performance's goal_max_rounds, pkg/gateway/rest_performance.go)
		// must change the resolved budget for BOTH owner kinds together,
		// in lockstep — never one without the other.
		configured := PlanningConfig{GoalMaxRounds: 45}
		if got := configured.EffectiveGoalMaxRounds(); got != 45 {
			t.Fatalf("EffectiveGoalMaxRounds() with GoalMaxRounds=45 = %d, want 45 (an operator-set global value must be honoured for a chat-owned goal)", got)
		}
		// Re-reading through the identical zero-argument accessor a second
		// time (standing in for the task-owned read site) must return the
		// exact same 45 — there is no separate task-side field to drift.
		if got := configured.EffectiveGoalMaxRounds(); got != 45 {
			t.Fatalf("EffectiveGoalMaxRounds() second read (task-owned) = %d, want 45 to match the chat-owned read", got)
		}
	})

	t.Run("a value below 1 is rejected at the boot-config boundary for the one shared field", func(t *testing.T) {
		// GOAL-FR-025's override argument is retired (D-E), but the budget
		// itself is still a real round count: PlanningConfig.GoalMaxRounds
		// below 1 must still fail validateBootConfig, since it is the one
		// field both owner kinds resolve through.
		cfg := minimalValidConfig()
		cfg.Planning = PlanningConfig{
			TaskMaxAttempts:      DefaultTaskMaxAttempts,
			GoalMaxRounds:        -1,
			PlanJudgeMaxRounds:   DefaultPlanJudgeMaxRounds,
			LoopMaxRuns:          DefaultLoopMaxRuns,
			IdleExpiryDays:       DefaultIdleExpiryDays,
			GlobalActiveLoopCap:  DefaultGlobalActiveLoopCap,
			CheckTimeoutSeconds:  DefaultCheckTimeoutSeconds,
			VerifierWindowTokens: DefaultVerifierWindowTokens,
		}
		if err := validateBootConfig(cfg); err == nil {
			t.Fatal("expected error for GoalMaxRounds=-1 (GOAL-FR-025's below-1 rejection applies to the one shared field even with the per-goal override retired)")
		}
	})
}
