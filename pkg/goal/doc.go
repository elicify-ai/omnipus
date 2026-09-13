// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package goal implements the goal entity store (ADR-086
// "Goal as a first-class entity", docs/internal/architecture/ADR-086-goal-as-a-first-class-entity.md,
// D1). A goal is a stored record in its own right, addressed by its own id,
// and is NOT represented as fields on a chat session or on a task — this
// package is the single home for that record, replacing the fourteen
// Goal*/goal_* fields that today live on session meta (`pkg/session`'s
// u5GoalFile / SessionMeta.Goal*) and on task.Task.Criteria.
//
// # Scope (wave S1 of the joint ADR-084/ADR-085/ADR-086 delivery)
//
// This package delivers GOAL-FR-001, FR-002, FR-003, FR-004, FR-006,
// FR-007, FR-008 and the store half of FR-027/FR-028 (terminal states are
// status transitions on a retained record, never field-zeroing erasure —
// D9). It does NOT wire activation into the agent loop, the keeper, the
// Judge, or any REST/WS surface — those are later, engine-lane waves (E4,
// E8, E12, E13, E14) that consume this package's exported accessors and
// mutators. It does NOT implement session retention (pkg/goal/retention.go,
// wave S3) or the surviving routing fields beyond what D1/D6 already fold
// into the owner reference (pkg/goal/routing.go, wave S5) — both are
// separate files in this same package, owned by different waves, calling
// only this file's exported Store methods and never reaching into its
// internals (see the joint delivery plan's §5 shared-file-chain row for
// pkg/goal/**).
//
// # No per-goal budget override (D-E / NQ-2, operator-decided 2026-09-11)
//
// GOAL-FR-025 as originally specified ("the budget MUST accept a per-goal
// override") is RETIRED. There is exactly one global goal-tries setting,
// under Settings -> Performance, governing a task-owned and a session-owned
// goal identically (D-D/D-E). This package's Goal.MaxRounds is therefore
// always stamped from the one global value by the caller that creates the
// record (an engine wave, not this package) — there is no override field
// anywhere on Goal, no override parameter on any constructor here, and
// config.PlanningConfig.EffectiveGoalMaxRounds keeps the signature it has
// today (no override argument). Do not add one.
//
// # No upgrade path (D-F, greenfield everywhere)
//
// This package assumes a fresh install. It builds no migration, no
// detection of orphaned pre-ADR-086 goal state on session meta, and no
// rescue path. An install upgrading across this change loses in-flight
// goals silently — that is a deliberate, operator-ratified consequence of
// D-F, not an oversight of this package.
//
// # Storage location and the entity precedent (FR-001)
//
// One JSON file per goal, at $OMNIPUS_HOME/entities/goals/<goal_id>.json,
// written via fileutil.WriteFileAtomic. This package is a thin,
// goal-specific wrapper over the generic pkg/entity store (ADR-054 D2/D3) —
// the same precedent pkg/agentstore already follows for
// $OMNIPUS_HOME/entities/agents/<id>.json. pkg/entity/store.go is reused
// AS-IS and is never forked: pkg/entity.Store[Goal] supplies the atomic
// per-entity file, the 64-shard in-process striped mutex, and the
// cross-process sidecar advisory flock; this package supplies only the
// Goal-shaped Accessors wiring and the goal-specific query surface
// (predicate.go's owner/active-state accessors) that a generic store has no
// way to know about.
//
// # Lock order (FR-008, R-33)
//
// The total lock order across this delivery is:
//
//	goalLock -> taskFileLock -> sessionLock -> cacheMu
//
// "goalLock" is this package's own locking (the pkg/entity striped mutex
// plus sidecar flock, taken inside Store.Create/Update/Delete). A caller
// that already holds an ADR-057 session shard
// (pkg/session/unified_lock.go's sessionLock(id)) or its cacheMu MUST NOT
// call into this store while holding it — the goal lock must be acquired
// and released strictly BEFORE a session lock is taken, never nested inside
// one. store.go exposes a swappable acquire/release observation seam
// (goalLockAcquireFn/goalLockReleaseFn, mirroring pkg/session's own FR-101
// sessionLockAcquireFn/sessionLockReleaseFn pattern) so a later wave that
// does hold both lock classes (S3's retention sweep) can make that ordering
// assertion observable in a test, the same way go test -race cannot: a race
// detector reports nothing for a lock-order inversion that does not happen
// to deadlock in the run under test.
//
// # Cross-process guarantee is POSIX-only (FR-008)
//
// fileutil.WithFlock (pkg/fileutil/flock_windows.go) is a documented no-op
// on Windows. On Windows only the in-process striped mutex protects
// concurrent goal-record writes; two Windows processes writing the same
// goal concurrently are not protected from each other. This mirrors
// pkg/entity's own documented scope decision (ADR-054 §5) and is not a
// regression introduced here.
//
// # Criteria and verdicts are reused, not reinvented (FR-003)
//
// Goal.Criteria and Goal.DoD are both []task.AcceptanceCriterion — the
// existing ADR-080 shared type, kept dependency-free by pkg/task
// specifically so a second consumer (this package, alongside pkg/plan)
// could reuse it without pulling in anything else from pkg/task. Neither
// list is ever persisted as a serialised string. Goal.LatestVerdict is
// *task.JudgeVerdict, the existing ADR-049 verdict type — this package does
// not mint a second verdict shape.
//
// # Criterion id namespace (FR-007)
//
// Three non-UUID criterion ids are reserved and MUST NOT be minted for a
// persisted criterion: "soft-tier-implicit" (pkg/agent/judge.go's ephemeral
// soft-tier fallback, never itself persisted), "goal-condition"
// (pkg/agent/goal_compile.go's compiled /goal condition criterion), and the
// "goal-dod-floor-" prefix (the compiler's built-in floor DoD items). This
// package declares that namespace (criteria.go's
// ReservedCriterionID*/IsReservedCriterionID) and enforces it on every
// criteria/dod list it persists — see criteria.go for why
// task.NormalizeCriteria alone does not close this gap (it only mints an id
// when one is absent; an explicit, caller-supplied id that collides with a
// reserved value passes through unless something else checks it).
package goal
