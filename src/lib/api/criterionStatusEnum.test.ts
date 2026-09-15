// criterionStatusEnum.test.ts — wave T1, GOAL-FR-037 / GOAL-FR-042 /
// GOAL-MV-7 / GOAL-SC-008 (docs/internal/specs/adr-084-086-joint-delivery-plan.md
// line 371). TypeScript/runtime half of the criterion-status vocabulary
// lock; the Go half lives in pkg/api/generated/contract_test.go
// (TestCriterionStatusEnumInAllFourContractCopies,
// TestContractCopies_AllHandSyncedCopiesAgree_Recursive) and
// pkg/task/criterion_test.go (TestNoFourthCriterionStatusValue).
//
// The goal spec's own file path for this test,
// src/lib/api/generated/asyncapi-criterion-status.test.ts, cannot exist:
// src/lib/api/generated/** is generated-only (Constraint #8) and never
// holds a hand-written test (C-61). This file is that relocation.
//
// ADR-084 revision 9 §10 withdrew the `unable_to_verify` third criterion
// outcome the in-tree judge spec still names throughout; the joint
// delivery plan's C-01 locks the status vocabulary at exactly three
// values: pending, met, unmet. These tests assert that lock BEHAVIOURALLY,
// against the REAL generated Zod schemas the SPA actually loads at the
// wire edge (imported from '@/lib/api/generated/schemas', the same module
// src/lib/ws.ts and src/store/chat.ts import for live WS-frame validation
// — never the openapi-typescript TS types alone, which erase at runtime
// and cannot be asserted against with a `.safeParse()` call).
//
// GOAL-SC-008 in particular requires "a runtime test asserts a
// GoalStatusFrame carrying met/unmet criteria survives SPA-edge zod
// validation" — the WsFrame-level tests below are that assertion: they
// parse a full discriminated-union frame, the same shape ws.ts receives
// off the wire, not just the inner GoalStatusFrame schema in isolation.

import { describe, it, expect } from 'vitest'
import {
  AcceptanceCriterion,
  AcceptanceCriterionInput,
  GoalStatusFrame,
  WsFrame,
} from '@/lib/api/generated/schemas'

// ── Fixtures ──────────────────────────────────────────────────────────────

function baseCriterion(status: string) {
  return {
    kind: 'prose',
    judgment: 'boolean',
    text: 'the button links to the pricing page',
    author: { kind: 'agent', id: 'jim' },
    status,
  }
}

function baseCriterionInput(status: string) {
  return {
    text: 'the button links to the pricing page',
    author: { kind: 'agent', id: 'jim' },
    status,
  }
}

function baseGoalStatusFrame(overrides: {
  criteriaStatus?: string
  dodStatus?: string
} = {}) {
  const { criteriaStatus = 'met', dodStatus = 'unmet' } = overrides
  return {
    type: 'goal_status',
    session_id: 'session-1',
    condition: 'active',
    round: 1,
    max_rounds: 5,
    latest_reason: 'round 1 complete',
    active_loops: 1,
    cap: 5,
    state: 'active',
    criteria: [baseCriterion(criteriaStatus)],
    dod: [baseCriterion(dodStatus)],
  }
}

// ── AcceptanceCriterion (Task/Plan response shape) ─────────────────────────

describe('AcceptanceCriterion.status — SPA-edge zod vocabulary lock', () => {
  it.each(['pending', 'met', 'unmet'])(
    'accepts the real status %s',
    (status) => {
      const result = AcceptanceCriterion.safeParse(baseCriterion(status))
      expect(result.success).toBe(true)
    },
  )

  it('rejects the withdrawn unable_to_verify status (ADR-084 rev 9 §10, C-01)', () => {
    const result = AcceptanceCriterion.safeParse(baseCriterion('unable_to_verify'))
    expect(result.success).toBe(false)
    if (!result.success) {
      const statusIssue = result.error.issues.find((i) => i.path.join('.') === 'status')
      expect(statusIssue).toBeDefined()
    }
  })

  it('rejects an arbitrary unknown status, not just unable_to_verify specifically', () => {
    const result = AcceptanceCriterion.safeParse(baseCriterion('in_progress'))
    expect(result.success).toBe(false)
  })

  it('differentiation: met and unmet produce distinctly successful, distinctly-shaped parses', () => {
    const met = AcceptanceCriterion.safeParse(baseCriterion('met'))
    const unmet = AcceptanceCriterion.safeParse(baseCriterion('unmet'))
    expect(met.success && unmet.success).toBe(true)
    if (met.success && unmet.success) {
      expect(met.data.status).toBe('met')
      expect(unmet.data.status).toBe('unmet')
      expect(met.data.status).not.toBe(unmet.data.status)
    }
  })
})

// ── AcceptanceCriterionInput (Task/Plan create/update request shape) ──────

describe('AcceptanceCriterionInput.status — SPA-edge zod vocabulary lock', () => {
  it.each(['pending', 'met', 'unmet'])(
    'accepts the real status %s',
    (status) => {
      const result = AcceptanceCriterionInput.safeParse(baseCriterionInput(status))
      expect(result.success).toBe(true)
    },
  )

  it('rejects the withdrawn unable_to_verify status', () => {
    const result = AcceptanceCriterionInput.safeParse(baseCriterionInput('unable_to_verify'))
    expect(result.success).toBe(false)
  })
})

// ── GoalStatusFrame.criteria[]/.dod[] — the asyncapi.yaml hand-synced copy ─
//
// GOAL-FR-042's most dangerous row: miss this hand-sync and every
// goal-status frame carrying a met/unmet criterion is DROPPED at the SPA
// edge with a green `make verify-contracts` (the check only compares
// generated artifacts against specs, never two hand-written spec copies
// against each other).

describe('GoalStatusFrame carrying met/unmet criteria — GOAL-SC-008', () => {
  it('a frame with a met criterion and an unmet dod item survives zod validation', () => {
    const result = GoalStatusFrame.safeParse(
      baseGoalStatusFrame({ criteriaStatus: 'met', dodStatus: 'unmet' }),
    )
    expect(result.success).toBe(true)
    if (result.success) {
      expect(result.data.criteria?.[0]?.status).toBe('met')
      expect(result.data.dod?.[0]?.status).toBe('unmet')
    }
  })

  it('a frame with a pending criterion also survives (all three real values, not just met/unmet)', () => {
    const result = GoalStatusFrame.safeParse(
      baseGoalStatusFrame({ criteriaStatus: 'pending', dodStatus: 'pending' }),
    )
    expect(result.success).toBe(true)
  })

  it('a frame carrying the withdrawn unable_to_verify status is DROPPED at the SPA edge, not silently accepted', () => {
    const result = GoalStatusFrame.safeParse(
      baseGoalStatusFrame({ criteriaStatus: 'unable_to_verify', dodStatus: 'unmet' }),
    )
    expect(result.success).toBe(false)
  })
})

// ── WsFrame — the actual discriminated union src/lib/ws.ts imports and
// validates every inbound WS message against (GOAL-SC-008's literal ask:
// "survives SPA-edge zod validation", not merely the inner schema alone).

describe('WsFrame(goal_status) — the real SPA-edge validator ws.ts/chat.ts use', () => {
  it('a full goal_status frame with met/unmet criteria round-trips through WsFrame', () => {
    const frame = baseGoalStatusFrame({ criteriaStatus: 'met', dodStatus: 'unmet' })
    const result = WsFrame.safeParse(frame)
    expect(result.success).toBe(true)
    if (result.success && result.data.type === 'goal_status') {
      expect(result.data.criteria?.[0]?.status).toBe('met')
      expect(result.data.dod?.[0]?.status).toBe('unmet')
    }
  })

  it('a goal_status frame carrying unable_to_verify fails the top-level WsFrame parse too', () => {
    const frame = baseGoalStatusFrame({ criteriaStatus: 'unable_to_verify' })
    const result = WsFrame.safeParse(frame)
    expect(result.success).toBe(false)
  })
})
