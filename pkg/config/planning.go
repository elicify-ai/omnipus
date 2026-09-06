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
	// DefaultVerifierWindowTokens is the last-N-tokens bound for the
	// transcript window fed to a verifier adjudication (ADR-052 FR-032,
	// operator interview 2026-07-21 confirmed N=20000).
	DefaultVerifierWindowTokens = 20000
	// DefaultGoalCompileWindowTokens is ADR-079 D1's operator-ratified
	// session-transcript-window bound fed into every /goal compile call
	// (initial, resumed, and repair) — deliberately MATCHES
	// DefaultVerifierWindowTokens (operator ratified 2026-09-06: maximum
	// recent-history coverage over the draft's smaller 6000, since goals are
	// frequently expressed against events set well back in a session).
	DefaultGoalCompileWindowTokens = 20000
	// DefaultSupervisionTurnTimeoutSeconds is FR-021's observation deadline:
	// how long after a supervision wake is armed the PlanSupervisor waits for
	// that turn to produce a correction before counting the attempt spent.
	// 600 s mirrors the package constant the plan engine used before this key
	// existed (pkg/agent/plan_engine.go defaultSupervisionTurnTimeout).
	DefaultSupervisionTurnTimeoutSeconds = 600
	// DefaultSupervisionMaxAttempts is FR-022's ceiling: supervision wakes
	// that produce NO valid correction, after which the plan terminates
	// failed(supervision_unavailable). Mirrors the plan engine's
	// defaultSupervisionMaxAttempts.
	DefaultSupervisionMaxAttempts = 3
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
	// VerifierWindowTokens bounds the last-N-tokens transcript window fed to
	// a verifier adjudication (ADR-052 FR-032, R3-2/GS-07): entries are read
	// via the session store's PartitionStore read path, rendered with the
	// existing transcript-to-context renderer, estimated with the existing
	// token estimator, and the LAST N tokens are kept. No per-entity override
	// exists yet (interview 2026-07-21 confirmed N=20000 globally;
	// per-verifier override is a noted future direction only).
	VerifierWindowTokens int `json:"verifier_window_tokens,omitempty"`
	// GoalCompileWindowTokens bounds the last-N-tokens session-transcript
	// window fed into a /goal compile call (ADR-079 D1): non-authoritative
	// background context, distinct from and subordinate to the goal
	// statement itself (INV-3). Read+rendered via the SAME
	// sessionWindowText/renderVerifierWindowText tail the Judge's own
	// VerifierWindowTokens feed uses — this field only supplies a different
	// budget at a different call site. No per-entity override (mirrors
	// VerifierWindowTokens's own no-per-entity-override rationale above).
	GoalCompileWindowTokens int `json:"goal_compile_window_tokens,omitempty"`
	// BootSweepBudgetSeconds bounds the wall-clock time the ADR-053 §5 boot
	// sweep may take to reconcile persisted non-terminal sessions to
	// failed(interrupted) at startup (FR-118 "within N s"). Zero inherits the
	// engine's DefaultBootSweepBudgetSeconds.
	BootSweepBudgetSeconds int `json:"boot_sweep_budget_seconds,omitempty"`
	// SnapshotMaxBytes caps a parked needs_input session's retained context
	// snapshot for the isNeedsInputReconstructable predicate (R§8.6 clause 4).
	// Zero inherits the engine's DefaultSnapshotMaxBytes.
	SnapshotMaxBytes int `json:"snapshot_max_bytes,omitempty"`
	// TokenBudget is the ADR-053 Phase-2 / D12 (R§8.3) app-level OVERALL token
	// budget: the ONE shared pool debited by ALL workloads (owner/member/
	// verifier/Judge) from provider-reported usage, deliberately NOT honoring
	// IsPrivilegedAgent (FR-171/FR-172). Sentinel 0 = unbounded (FR-175); an
	// unset budget runs unbounded with a persistent Usage-screen advisory. The
	// ceiling is restart-gated (FR-177 — a live change would straddle two
	// budgets, the N-15 hazard); the live lever for runaway spend is the
	// existing Stop/cancel cascade, NOT a live token cut. This field supersedes
	// NFR-1's "no token/money fields" for THIS one overall budget (D12 converts
	// SEC-26's USD cap → tokens because cost isn't reliably measurable).
	TokenBudget int64 `json:"token_budget,omitempty"`
	// SupervisionTurnTimeoutSeconds is FR-021's supervision observation
	// deadline: how long the PlanSupervisor waits on an armed wake before the
	// attempt counts as spent. Overridden per-plan by
	// plan.PlanBounds.SupervisionTurnTimeoutSeconds. Zero inherits
	// DefaultSupervisionTurnTimeoutSeconds via
	// EffectiveSupervisionTurnTimeoutSeconds.
	SupervisionTurnTimeoutSeconds int `json:"supervision_turn_timeout_seconds,omitempty"`
	// SupervisionMaxAttempts is FR-022's ceiling on supervision turns that
	// produce no valid correction; exhausting it terminates the plan
	// failed(supervision_unavailable). Overridden per-plan by
	// plan.PlanBounds.SupervisionMaxAttempts. Zero inherits
	// DefaultSupervisionMaxAttempts via EffectiveSupervisionMaxAttempts.
	SupervisionMaxAttempts int `json:"supervision_max_attempts,omitempty"`
}

// EffectiveBootSweepBudgetSeconds resolves the boot-sweep budget (FR-118):
// this config's BootSweepBudgetSeconds when >=1, else 0 (the engine substitutes
// its own DefaultBootSweepBudgetSeconds on a zero/<=0 return, keeping the
// config struct free of an agent-package import).
func (c PlanningConfig) EffectiveBootSweepBudgetSeconds() int {
	if c.BootSweepBudgetSeconds >= 1 {
		return c.BootSweepBudgetSeconds
	}
	return 0
}

// EffectiveSnapshotMaxBytes resolves the reconstructability snapshot cap
// (R§8.6 clause 4): this config's SnapshotMaxBytes when >=1, else 0 (the
// engine substitutes its own DefaultSnapshotMaxBytes on a zero/<=0 return).
func (c PlanningConfig) EffectiveSnapshotMaxBytes() int64 {
	if c.SnapshotMaxBytes >= 1 {
		return int64(c.SnapshotMaxBytes)
	}
	return 0
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

// EffectiveTokenBudget resolves the app-level OVERALL token budget ceiling
// (D12/R§8.3/FR-175): returns the configured TokenBudget verbatim, since 0 IS
// the unbounded sentinel (not a "fall back to a default" case). A negative
// value is clamped to 0 (unbounded) defensively. The ceiling is restart-gated
// (FR-177): the caller reads this ONCE at boot to construct the TokenBudget
// pool; later config changes do NOT live-reload the ceiling (a live change
// would straddle two budgets). The live lever for runaway spend is the existing
// Stop/cancel cascade, not a live token cut.
func (c PlanningConfig) EffectiveTokenBudget() int64 {
	if c.TokenBudget < 0 {
		return 0
	}
	return c.TokenBudget
}

// EffectiveSupervisionTurnTimeoutSeconds resolves FR-021's supervision
// observation deadline (FR-9 resolution order): a non-nil, >=1 per-plan Bounds
// override wins; otherwise this config's SupervisionTurnTimeoutSeconds;
// otherwise DefaultSupervisionTurnTimeoutSeconds. Safe to call against a
// zero-value PlanningConfig.
//
// Returns SECONDS, not a time.Duration, so pkg/config stays free of any
// scheduling semantics — the plan engine converts.
func (c PlanningConfig) EffectiveSupervisionTurnTimeoutSeconds(override *int) int {
	if override != nil && *override >= 1 {
		return *override
	}
	if c.SupervisionTurnTimeoutSeconds >= 1 {
		return c.SupervisionTurnTimeoutSeconds
	}
	return DefaultSupervisionTurnTimeoutSeconds
}

// EffectiveSupervisionMaxAttempts resolves FR-022's no-correction attempt
// ceiling (FR-9 resolution order): a non-nil, >=1 per-plan Bounds override
// wins; otherwise this config's SupervisionMaxAttempts; otherwise
// DefaultSupervisionMaxAttempts.
func (c PlanningConfig) EffectiveSupervisionMaxAttempts(override *int) int {
	if override != nil && *override >= 1 {
		return *override
	}
	if c.SupervisionMaxAttempts >= 1 {
		return c.SupervisionMaxAttempts
	}
	return DefaultSupervisionMaxAttempts
}

// EffectiveVerifierWindowTokens resolves the verifier transcript-window
// token bound (FR-032): this config's VerifierWindowTokens when >=1, else
// DefaultVerifierWindowTokens. Same no-per-entity-override rationale as
// EffectiveGoalMaxRounds/EffectiveLoopMaxRuns above (per-verifier override is
// a noted future direction only, not implemented) — the resolved value is
// not currently snapshotted anywhere since a verifier session is fresh-built
// per adjudication (FR-011), unlike /goal or /loop's session-lifetime bound.
func (c PlanningConfig) EffectiveVerifierWindowTokens() int {
	if c.VerifierWindowTokens >= 1 {
		return c.VerifierWindowTokens
	}
	return DefaultVerifierWindowTokens
}

// EffectiveGoalCompileWindowTokens resolves the /goal compile
// session-transcript-window token bound (ADR-079 D1): this config's
// GoalCompileWindowTokens when >=1, else DefaultGoalCompileWindowTokens.
// Same no-per-entity-override rationale as EffectiveVerifierWindowTokens.
func (c PlanningConfig) EffectiveGoalCompileWindowTokens() int {
	if c.GoalCompileWindowTokens >= 1 {
		return c.GoalCompileWindowTokens
	}
	return DefaultGoalCompileWindowTokens
}
