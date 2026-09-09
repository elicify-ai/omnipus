// ADR-052 Wave 2 — wire-fixture contract tests (agent "qa-spa").
//
// Zod round-trip tests against the REGENERATED contract schemas in
// src/lib/api/generated/schemas.ts, covering the wire surface this feature
// adds/modifies (Constraint #8): Task.cancel_reason (FR-028), Plan.failed_reason
// value set (FR-015/016), Session `type: "verifier"` (FR-036), Agent.memory_enabled
// (FR-039), and the AcceptanceCriterion `behavior` criterion payload incl.
// min/max/scope (FR-034). Also asserts the FR-038 soul/rubric unification —
// `rubric` MUST NOT exist as a field on Agent / AgentUpdateRequest.
//
// STRICT OWNERSHIP NOTE: this file only READS the generated schemas — it does
// not modify any production/source file. New file, does not touch existing
// tests.
//
// Traces to: docs/internal/specs/autonomous-agent-plan-execution-spec.md
//   FR-015 (line 671), FR-016 (line 672), FR-023 (line 679), FR-028 (line 684),
//   FR-034 (line 690), FR-036 (line 692), FR-038 (line 694), FR-039 (line 695)
//   DS-7 (line 622) for behavior-criterion validation rows.

import { describe, it, expect } from 'vitest'
import {
  Task as TaskSchema,
  Plan as PlanSchema,
  Session as SessionSchema,
  Agent as AgentSchema,
  AgentUpdateRequest as AgentUpdateRequestSchema,
  AcceptanceCriterion as AcceptanceCriterionSchema,
} from '@/lib/api/generated/schemas'

// ── Fixture builders ────────────────────────────────────────────────────────
// Minimal-but-complete fixtures satisfying each schema's required fields, so
// each test varies ONLY the field under test (a differentiation-safe base).

function baseTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'task-1',
    title: 'Ship the thing',
    action: 'llm',
    status: 'failed',
    workspace_id: 'ws-1',
    owner: 'jim',
    created_by: 'jim',
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    ...overrides,
  }
}

function basePlan(overrides: Record<string, unknown> = {}) {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Ship the epic',
    state: 'failed',
    owner_agent_id: 'jim',
    owner: 'jim',
    created_by: 'jim',
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    ...overrides,
  }
}

function baseSessionStats() {
  return {
    tokens_in: 0,
    tokens_out: 0,
    tokens_total: 0,
    cost: 0,
    tool_calls: 0,
    message_count: 0,
  }
}

function baseSession(overrides: Record<string, unknown> = {}) {
  return {
    id: 'sess-1',
    agent_id: 'jim',
    title: 'Verification of plan-1 member T1',
    status: 'active',
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    stats: baseSessionStats(),
    channel: 'internal',
    partitions: [],
    ...overrides,
  }
}

function baseAgent(overrides: Record<string, unknown> = {}) {
  return {
    id: 'judge',
    name: 'Judge',
    type: 'system',
    locked: true,
    status: 'active',
    soul: 'You are a skeptical, evidence-first verifier.',
    timeout_seconds: 120,
    max_tool_iterations: 10,
    // A-CONTRACT (ADR-068 FR-038): needs_model is required on every Agent.
    needs_model: false,
    ...overrides,
  }
}

function baseCriterion(overrides: Record<string, unknown> = {}) {
  return {
    kind: 'behavior',
    // ADR-080 D-TYPES — `judgment` is required on the canonical
    // AcceptanceCriterion schema; `behavior` infers `quantitative`
    // server-side, but any valid enum value satisfies this fixture's own
    // schema (this file is a fixed `behavior`-kind, `judgment` is not the
    // field under test here).
    judgment: 'quantitative',
    text: 'Called web_search at least 5 times',
    author: { kind: 'agent', id: 'jim' },
    status: 'pending',
    ...overrides,
  }
}

// ── Task.cancel_reason (FR-028) ─────────────────────────────────────────────

describe('ADR-052 FR-028 — Task.cancel_reason wire contract', () => {
  it('accepts a cancelled task carrying cancel_reason:"stopped_by_user"', () => {
    const result = TaskSchema.safeParse(baseTask({ cancel_reason: 'stopped_by_user' }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.cancel_reason).toBe('stopped_by_user')
  })

  it('accepts a genuinely-failed task with cancel_reason absent (differentiator: absent != cancelled)', () => {
    const withoutOverride = baseTask()
    delete (withoutOverride as { cancel_reason?: unknown }).cancel_reason
    const result = TaskSchema.safeParse(withoutOverride)
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.cancel_reason).toBeUndefined()
  })

  it('accepts cancel_reason: null (restart-cleared state, mirrors Plan.failed_reason clearing)', () => {
    const result = TaskSchema.safeParse(baseTask({ cancel_reason: null }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.cancel_reason).toBeNull()
  })

  it('rejects an invented reason string not in the closed literal', () => {
    const result = TaskSchema.safeParse(baseTask({ cancel_reason: 'timeout' }))
    expect(result.success).toBe(false)
  })

  it('rejects a plausible-but-wrong sibling value ("user_cancelled") — literal must be exact', () => {
    const result = TaskSchema.safeParse(baseTask({ cancel_reason: 'user_cancelled' }))
    expect(result.success).toBe(false)
  })

  it('rejects a non-string cancel_reason', () => {
    const result = TaskSchema.safeParse(baseTask({ cancel_reason: 1 }))
    expect(result.success).toBe(false)
  })
})

// ── Plan.failed_reason (FR-015/016) ─────────────────────────────────────────

describe('ADR-052 FR-015/016 — Plan.failed_reason wire contract', () => {
  it.each([
    ['stopped_by_user'],
    ['judge_rounds_exhausted'],
    ['idle_expired'],
  ] as const)('accepts the closed enum value %s', (reason) => {
    const result = PlanSchema.safeParse(basePlan({ failed_reason: reason }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.failed_reason).toBe(reason)
  })

  it('the three accepted reasons round-trip to three DIFFERENT values (not collapsed to one)', () => {
    const parsed = (['stopped_by_user', 'judge_rounds_exhausted', 'idle_expired'] as const).map((r) => {
      const result = PlanSchema.safeParse(basePlan({ failed_reason: r }))
      return result.success ? result.data.failed_reason : 'PARSE_FAILED'
    })
    expect(new Set(parsed).size).toBe(3)
  })

  it('accepts a plan with failed_reason absent (draft/approved/running/done plans)', () => {
    const withoutReason = basePlan({ state: 'running' })
    delete (withoutReason as { failed_reason?: unknown }).failed_reason
    const result = PlanSchema.safeParse(withoutReason)
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.failed_reason).toBeUndefined()
  })

  it('rejects an out-of-enum reason string', () => {
    const result = PlanSchema.safeParse(basePlan({ failed_reason: 'cancelled_by_admin' }))
    expect(result.success).toBe(false)
  })

  it('rejects null for failed_reason (optional-but-not-nullish, unlike Task.cancel_reason)', () => {
    const result = PlanSchema.safeParse(basePlan({ failed_reason: null }))
    expect(result.success).toBe(false)
  })
})

// ── Session `type: "verifier"` (FR-036) ─────────────────────────────────────

describe('ADR-052 FR-036 — Session type "verifier" wire contract', () => {
  it.each([
    ['chat'],
    ['task'],
    ['channel'],
    ['scheduled'],
    ['heartbeat'],
    ['verifier'],
  ] as const)('accepts session type %s', (type) => {
    const result = SessionSchema.safeParse(baseSession({ type }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.type).toBe(type)
  })

  it('a verifier session and a task session parse to DIFFERENT type values (no collapsing)', () => {
    const verifier = SessionSchema.safeParse(baseSession({ type: 'verifier' }))
    const task = SessionSchema.safeParse(baseSession({ type: 'task' }))
    expect(verifier.success && task.success).toBe(true)
    if (verifier.success && task.success) {
      expect(verifier.data.type).not.toBe(task.data.type)
    }
  })

  it('rejects an unrecognized session type (e.g. a typo like "judge" instead of "verifier")', () => {
    const result = SessionSchema.safeParse(baseSession({ type: 'judge' }))
    expect(result.success).toBe(false)
  })

  it('accepts a session with type omitted (legacy sessions default to chat client-side, not enforced here)', () => {
    const withoutType = baseSession()
    delete (withoutType as { type?: unknown }).type
    const result = SessionSchema.safeParse(withoutType)
    expect(result.success).toBe(true)
  })
})

// ── Agent.memory_enabled (FR-039) ───────────────────────────────────────────

describe('ADR-052 FR-039 — Agent.memory_enabled wire contract', () => {
  it('defaults to true when omitted (ordinary agents keep memory on)', () => {
    const withoutField = baseAgent({ type: 'Main', locked: false })
    delete (withoutField as { memory_enabled?: unknown }).memory_enabled
    const result = AgentSchema.safeParse(withoutField)
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.memory_enabled).toBe(true)
  })

  it('the seeded Judge is explicitly false (memory OFF, FR-039) — differs from the default-true case', () => {
    const result = AgentSchema.safeParse(baseAgent({ memory_enabled: false }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.memory_enabled).toBe(false)
  })

  it('explicit true round-trips as true (differentiator vs explicit false)', () => {
    const result = AgentSchema.safeParse(baseAgent({ memory_enabled: true }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.memory_enabled).toBe(true)
  })

  it('rejects a non-boolean memory_enabled', () => {
    const result = AgentSchema.safeParse(baseAgent({ memory_enabled: 'yes' }))
    expect(result.success).toBe(false)
  })

  it('AgentUpdateRequest also carries memory_enabled as a plain optional boolean', () => {
    const okTrue = AgentUpdateRequestSchema.safeParse({ memory_enabled: true })
    const okFalse = AgentUpdateRequestSchema.safeParse({ memory_enabled: false })
    const bad = AgentUpdateRequestSchema.safeParse({ memory_enabled: 'nope' })
    expect(okTrue.success).toBe(true)
    expect(okFalse.success).toBe(true)
    expect(bad.success).toBe(false)
  })
})

// ── FR-038 — soul/rubric unification: `rubric` MUST be gone from the wire ───

describe('ADR-052 FR-038 — Agent.rubric field is DELETED (soul/rubric unification)', () => {
  it('Agent schema has no "rubric" key in its shape', () => {
    const shape = (AgentSchema as unknown as { shape: Record<string, unknown> }).shape
    expect(Object.keys(shape)).not.toContain('rubric')
  })

  it('AgentUpdateRequest schema has no "rubric" key in its shape', () => {
    const shape = (AgentUpdateRequestSchema as unknown as { shape: Record<string, unknown> }).shape
    expect(Object.keys(shape)).not.toContain('rubric')
  })

  it('Agent schema DOES carry "soul" — the unified prompt/rubric concept (FR-038)', () => {
    const shape = (AgentSchema as unknown as { shape: Record<string, unknown> }).shape
    expect(Object.keys(shape)).toContain('soul')
  })
})

// ── AcceptanceCriterion `behavior` kind payload (FR-034) — DS-7 dataset ─────

describe('ADR-052 FR-034 — AcceptanceCriterion behavior-kind payload contract (DS-7)', () => {
  it('accepts a full, explicit behavior payload', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'web_search', min_count: 5, max_count: 5, scope: 'attempt' } }),
    )
    expect(result.success).toBe(true)
    if (result.success) {
      expect(result.data.behavior).toEqual({
        tool: 'web_search',
        min_count: 5,
        max_count: 5,
        scope: 'attempt',
      })
    }
  })

  it('applies documented defaults: min_count=1, scope="task_session" when only "tool" is given', () => {
    const result = AcceptanceCriterionSchema.safeParse(baseCriterion({ behavior: { tool: 'send_message' } }))
    expect(result.success).toBe(true)
    if (result.success) {
      expect(result.data.behavior?.min_count).toBe(1)
      expect(result.data.behavior?.scope).toBe('task_session')
      expect(result.data.behavior?.max_count).toBeUndefined()
    }
  })

  it('a differentiation pair: two different tool names round-trip to two different values', () => {
    const a = AcceptanceCriterionSchema.safeParse(baseCriterion({ behavior: { tool: 'web_search' } }))
    const b = AcceptanceCriterionSchema.safeParse(baseCriterion({ behavior: { tool: 'bash' } }))
    expect(a.success && b.success).toBe(true)
    if (a.success && b.success) {
      expect(a.data.behavior?.tool).not.toBe(b.data.behavior?.tool)
    }
  })

  it.each([
    ['attempt'],
    ['task_session'],
  ] as const)('accepts scope value %s', (scope) => {
    const result = AcceptanceCriterionSchema.safeParse(baseCriterion({ behavior: { tool: 'web_search', scope } }))
    expect(result.success).toBe(true)
    if (result.success) expect(result.data.behavior?.scope).toBe(scope)
  })

  it('rejects an out-of-enum scope value', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'web_search', scope: 'plan_session' } }),
    )
    expect(result.success).toBe(false)
  })

  it('rejects a negative min_count', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'web_search', min_count: -1 } }),
    )
    expect(result.success).toBe(false)
  })

  it('rejects a negative max_count', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'web_search', max_count: -1 } }),
    )
    expect(result.success).toBe(false)
  })

  it('accepts min_count=0 AND max_count=0 ("never call this tool" — spec-documented legal case)', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'delete_file', min_count: 0, max_count: 0 } }),
    )
    expect(result.success).toBe(true)
    if (result.success) {
      expect(result.data.behavior?.min_count).toBe(0)
      expect(result.data.behavior?.max_count).toBe(0)
    }
  })

  it('rejects a missing "tool" field (required)', () => {
    const result = AcceptanceCriterionSchema.safeParse(baseCriterion({ behavior: { min_count: 1 } }))
    expect(result.success).toBe(false)
  })

  it('rejects an empty-string "tool"', () => {
    const result = AcceptanceCriterionSchema.safeParse(baseCriterion({ behavior: { tool: '' } }))
    expect(result.success).toBe(false)
  })

  // ── Two FR-034 prose rules the OpenAPI `additionalProperties: false` /
  //    "max_count >= min_count" constraint (AcceptanceCriterion.yaml:82-85,
  //    ADR-052 spec DS-7 row 6) DOCUMENTS but which openapi-zod-client cannot
  //    express as generated Zod code (no .strict()/.superRefine() emitted for
  //    either additionalProperties:false or a cross-field business rule from
  //    prose). These two tests assert the SPEC'd behavior and are marked
  //    it.fails as inverted canaries for a known contract-fidelity gap
  //    (tracked on the ADR-052 fix-wave ledger, not silently worked around):
  //    the moment a contracts fix adds .strict()/.superRefine() coverage,
  //    they flip to failing-as-fails, forcing removal of the .fails marker
  //    and promotion to ordinary assertions.
  it.fails('[KNOWN GAP — contract fidelity] rejects max_count < min_count per DS-7 row 6', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'web_search', min_count: 5, max_count: 2 } }),
    )
    expect(result.success).toBe(false)
  })

  it.fails('[KNOWN GAP — contract fidelity] rejects an unknown field in the behavior payload (additionalProperties:false, AcceptanceCriterion.yaml:81-82)', () => {
    const result = AcceptanceCriterionSchema.safeParse(
      baseCriterion({ behavior: { tool: 'web_search', bogus_field: 'nope' } }),
    )
    expect(result.success).toBe(false)
  })
})
