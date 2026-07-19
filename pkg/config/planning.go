// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// Default bounds for PlanningConfig (ADR-049 D7, spec Part A §G "Config
// bounds"). Applied by the boot validator (validateBootConfig, validator.go)
// whenever the corresponding field is zero, and used directly by DefaultConfig
// (defaults.go) to populate a fresh install's config.json.
const (
	DefaultTaskMaxAttempts     = 3
	DefaultGoalMaxRounds       = 20
	DefaultPlanJudgeMaxRounds  = 20
	DefaultLoopMaxRuns         = 100
	DefaultIdleExpiryDays      = 7
	DefaultGlobalActiveLoopCap = 16
	DefaultCheckTimeoutSeconds = 60
)

// PlanningConfig holds the global bounds for the Planning & Goals epic's
// loop-shaped constructs: Plan judge rounds, /goal, /loop, per-task attempt
// ceilings, the global active-loop admission cap (R5), and the per-check
// timeout. Per-entity overrides (plan.Plan.Bounds, task.Task.MaxAttempts)
// take precedence over these global values — see the Effective* resolver
// methods below (FR-9). Deliberately has NO token/money fields (NFR-1).
type PlanningConfig struct {
	// TaskMaxAttempts is the default attempt ceiling before a standalone
	// task's goal loop wakes its owner. Overridden per-task by
	// task.Task.MaxAttempts (nil ⇒ inherit this default).
	TaskMaxAttempts int `json:"task_max_attempts,omitempty"`
	// GoalMaxRounds bounds a /goal session loop's round count.
	GoalMaxRounds int `json:"goal_max_rounds,omitempty"`
	// PlanJudgeMaxRounds is the default plan-judge round ceiling before a
	// running Plan fails with failed_reason=judge_rounds_exhausted.
	// Overridden per-plan by plan.PlanBounds.PlanJudgeMaxRounds.
	PlanJudgeMaxRounds int `json:"plan_judge_max_rounds,omitempty"`
	// LoopMaxRuns bounds a /loop session's run count.
	LoopMaxRuns int `json:"loop_max_runs,omitempty"`
	// IdleExpiryDays is the calendar brake: a loop-shaped entity with no
	// activity for this many days is force-terminated (e.g.
	// failed_reason=idle_expired for a Plan). Overridden per-plan by
	// plan.PlanBounds.IdleExpiryDays.
	IdleExpiryDays int `json:"idle_expiry_days,omitempty"`
	// GlobalActiveLoopCap caps the number of simultaneously active loops —
	// running plans + active /goal sessions + enabled /loop jobs (R5) —
	// across the whole install.
	GlobalActiveLoopCap int `json:"global_active_loop_cap,omitempty"`
	// CheckTimeoutSeconds bounds how long a single `kind: check` acceptance
	// criterion's command may run before it is killed and judged unmet
	// (evidence.TimedOut=true).
	CheckTimeoutSeconds int `json:"check_timeout_seconds,omitempty"`
}

// EffectiveTaskMaxAttempts resolves the attempt ceiling (FR-9): a non-nil,
// >=1 per-task override wins; otherwise falls back to this config's
// TaskMaxAttempts (itself defaulting to DefaultTaskMaxAttempts when <1, so
// this resolver is safe to call even against a zero-value PlanningConfig).
func (c PlanningConfig) EffectiveTaskMaxAttempts(override *int) int {
	if override != nil && *override >= 1 {
		return *override
	}
	if c.TaskMaxAttempts >= 1 {
		return c.TaskMaxAttempts
	}
	return DefaultTaskMaxAttempts
}

// EffectivePlanJudgeMaxRounds resolves the plan-judge round ceiling (FR-9): a
// non-nil, >=1 per-plan Bounds override wins; otherwise falls back to this
// config's PlanJudgeMaxRounds (defaulting to DefaultPlanJudgeMaxRounds).
func (c PlanningConfig) EffectivePlanJudgeMaxRounds(override *int) int {
	if override != nil && *override >= 1 {
		return *override
	}
	if c.PlanJudgeMaxRounds >= 1 {
		return c.PlanJudgeMaxRounds
	}
	return DefaultPlanJudgeMaxRounds
}

// EffectiveIdleExpiryDays resolves the idle-expiry calendar brake (FR-9): a
// non-nil, >=1 per-plan Bounds override wins; otherwise falls back to this
// config's IdleExpiryDays (defaulting to DefaultIdleExpiryDays).
func (c PlanningConfig) EffectiveIdleExpiryDays(override *int) int {
	if override != nil && *override >= 1 {
		return *override
	}
	if c.IdleExpiryDays >= 1 {
		return c.IdleExpiryDays
	}
	return DefaultIdleExpiryDays
}

// EffectiveGoalMaxRounds resolves the /goal round ceiling (FR-9, FR-067):
// this config's GoalMaxRounds when >=1, else DefaultGoalMaxRounds. No
// per-entity override source exists for /goal (a session-scoped chat
// command, not a stored entity with its own Bounds) — the resolved value is
// snapshotted onto the session's UnifiedMeta.GoalMaxRounds at `/goal` set
// time so a later config change never retroactively changes an
// already-running goal's bound.
func (c PlanningConfig) EffectiveGoalMaxRounds() int {
	if c.GoalMaxRounds >= 1 {
		return c.GoalMaxRounds
	}
	return DefaultGoalMaxRounds
}

// EffectiveLoopMaxRuns resolves the /loop run ceiling (FR-9, FR-072): this
// config's LoopMaxRuns when >=1, else DefaultLoopMaxRuns. Same
// no-per-entity-override rationale as EffectiveGoalMaxRounds above — the
// resolved value is snapshotted onto UnifiedMeta.LoopMaxRuns at `/loop` set
// time.
func (c PlanningConfig) EffectiveLoopMaxRuns() int {
	if c.LoopMaxRuns >= 1 {
		return c.LoopMaxRuns
	}
	return DefaultLoopMaxRuns
}
