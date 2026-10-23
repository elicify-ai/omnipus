import { describe, expect, it } from 'vitest'
import type { z } from 'zod'
import { Session, Message, SessionLifecycleRecord, DelegateSessionResponse, DelegatePeekResponse } from '@/lib/api/generated/schemas'
import { DoneStats, ReplayMessageFrame, SubagentStateFrame, SubagentEndFrame } from '@/lib/api/generated/ws-schemas'

// DESIGN Decision 1's exact contract inventory. All schemas are generated;
// fixtures are data, never alternate handwritten transport interfaces.
const NOW = '2026-10-22T00:00:00Z'
const stats = { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 }
function session(reason: unknown) {
  return { id: 'helper', type: 'delegate', agent_id: 'mia', title: 'Helper', status: 'interrupted',
    lifecycle_state: 'interrupted', interruption_reason: reason, created_at: NOW, updated_at: NOW,
    channel: 'webchat', partitions: [], stats }
}
function lifecycle(reason?: unknown) {
  return { session_id: 'helper', generation: 1, state: 'interrupted', terminal: true, owner_scope_kind: 'parent_session',
    owner_scope_id: 'parent', workspace_id: 'ws', agent_id: 'mia', is_3p: false,
    undelivered_message_ids: [], created_at: NOW, updated_at: NOW,
    ...(reason === undefined ? {} : { interruption_reason: reason }) }
}
function preserved(schema: z.ZodType, payload: unknown, reason: string) {
  const parsed = schema.safeParse(payload)
  expect(parsed.success, JSON.stringify(parsed)).toBe(true)
  if (!parsed.success) throw new Error('generated validator rejected a specified Interrupted payload')
  const value = JSON.parse(JSON.stringify(parsed.data))
  expect(value.interruption_reason).toBe(reason)
}

describe('crash-recovery generated wire reason parity', () => {
  it.each(['tool_limit', 'reconciliation_required'])('preserves %s in REST Session', (reason) => preserved(Session, session(reason), reason))
  it.each(['tool_limit', 'reconciliation_required'])('preserves %s in REST Message', (reason) => preserved(Message,
    { id: 'message', agent_id: 'mia', timestamp: NOW, status: 'interrupted', interruption_reason: reason }, reason))
  it.each(['tool_limit', 'reconciliation_required'])('preserves %s in replay', (reason) => preserved(ReplayMessageFrame,
    { type: 'replay_message', session_id: 'helper', id: 'message', role: 'assistant', content: 'Interrupted', interruption_reason: reason }, reason))
  it.each(['tool_limit', 'reconciliation_required'])('preserves %s in live done stats', (reason) => preserved(DoneStats,
    { interruption_reason: reason, turn_failed: false }, reason))
  it.each(['tool_limit', 'reconciliation_required'])('accepts ended lifecycle %s, including terminal=true', (reason) => preserved(SessionLifecycleRecord, lifecycle(reason), reason))
  it.each(['tool_limit', 'reconciliation_required'])('accepts live state %s', (reason) => preserved(SubagentStateFrame,
    { type: 'subagent_state', session_id: 'parent', child_session_id: 'helper', span_id: 'span', state: 'interrupted', created_at: NOW, interruption_reason: reason }, reason))
  // The DESIGN contract inventory also names the real delegate responses.
  // An ended projection must not rewrite the admission's queued/running result.
  it.each(['tool_limit', 'reconciliation_required'])('preserves %s in delegate session response', (reason) => preserved(DelegateSessionResponse,
    { session_id: 'helper', generation: 1, is_3p: false, state: 'interrupted', interruption_reason: reason }, reason))
  it.each(['tool_limit', 'reconciliation_required'])('preserves %s in delegate peek response', (reason) => preserved(DelegatePeekResponse,
    { session_id: 'helper', state: 'interrupted', interruption_reason: reason }, reason))
  it.each(['queued', 'running'])('keeps actual %s delegate admission valid without a reason', (state) => {
    const parsed = DelegateSessionResponse.safeParse({ session_id: 'helper', generation: 1, is_3p: false, state })
    expect(parsed.success).toBe(true)
    if (!parsed.success) throw new Error('generated validator rejected a real admission state')
    expect(parsed.data.state).toBe(state)
    expect(Object.hasOwn(parsed.data, 'interruption_reason')).toBe(false)
  })
  it.each(['tool_limit', 'reconciliation_required'])('accepts truthful live end cause %s', (reason) => {
    const value = { type: 'subagent_end', session_id: 'parent', span_id: 'span', status: 'interrupted', reason }
    expect(SubagentEndFrame.safeParse(value).success).toBe(true)
  })

  // Decision D: "the producer guarantees the relation; the client-side generated validators stay structural"
  // Newly recorded Interrupted requires a reason at the Go public Persist boundary.
  it.each([null, 'restart', 'invented'])('rejects invalid durable reason %s', (reason) => {
    expect(SessionLifecycleRecord.safeParse(lifecycle('tool_limit')).success).toBe(true)
    expect(SessionLifecycleRecord.safeParse(lifecycle(reason)).success).toBe(false)
  })
  // Decision D: "The rejection of invalid pairings is enforced where the payloads are built"
  // A reason on Running is rejected by the Go public Persist oracle, not this structural reader.
  it.each([null, 'invented'])('rejects invalid live DoneStats reason %s', (interruption_reason) => {
    expect(DoneStats.safeParse({ interruption_reason: 'tool_limit' }).success).toBe(true)
    expect(DoneStats.safeParse({ interruption_reason }).success).toBe(false)
  })
  // Decision D: "invalid pairings are rejected by the producer's runtime validation"
  // Interrupted execution-key omission is asserted on actual REST JSON bytes in Go.
  it('keeps ordinary stopped and genuine failed controls valid without a reason', () => {
    const base = { ...lifecycle(), terminal: false, state: 'stopped' }
    expect(SessionLifecycleRecord.safeParse(base).success).toBe(true)
    expect(SubagentStateFrame.safeParse({ type: 'subagent_state', session_id: 'parent', span_id: 'span', state: 'failed', created_at: NOW }).success).toBe(true)
  })
})
