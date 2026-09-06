// pendingAskReconcile.test.ts — askuserquestion-tool-spec v3 US-6 S1/FR-9.
//
// Unit tests for the dedicated session_state pending_asks reconcile function
// (extracted from chat.ts's inline session_state case, mirroring the
// toolApproval reconcileWithSessionState pattern). Covers: hydration from a
// snapshot, clearing a card that resolved while disconnected, idempotent
// duplicate hydration, and the race with a live terminal frame.

import { describe, it, expect } from 'vitest'
import { reconcilePendingAsks, type PendingAskBucket } from './pendingAskReconcile'
import type { AskUserQuestionCard } from '@/lib/api/generated/asyncapi-types'

function makeCard(overrides: Partial<AskUserQuestionCard> = {}): AskUserQuestionCard {
  return {
    card_id: 'ask_1',
    session_id: 's1',
    agent_id: 'mia',
    status: 'pending',
    created_at: '2026-09-06T12:00:00Z',
    questions: [
      {
        header: 'Scope',
        question: 'Which emails should this goal cover?',
        options: [{ label: 'Only unanswered' }, { label: 'All customer email' }],
      },
    ],
    ...overrides,
  }
}

/** Applies a reconcile result to a sessions map the way chat.ts does (per-session pendingAsk write). */
function apply(
  sessions: Record<string, PendingAskBucket>,
  changes: Record<string, AskUserQuestionCard | null>,
): Record<string, PendingAskBucket> {
  const next: Record<string, PendingAskBucket> = { ...sessions }
  for (const [sid, card] of Object.entries(changes)) {
    next[sid] = { ...(next[sid] ?? {}), pendingAsk: card }
  }
  return next
}

describe('reconcilePendingAsks — hydrate from snapshot', () => {
  it('hydrates every snapshot card into its session, including sessions with no bucket yet', () => {
    const c1 = makeCard()
    const c2 = makeCard({ card_id: 'ask_2', session_id: 's2' })
    // s1 exists but empty; s2 has no bucket at all.
    const changes = reconcilePendingAsks([c1, c2], { s1: {} })
    expect(changes).toEqual({ s1: c1, s2: c2 })
  })

  it('the server copy wins over a stale local pending copy of the same card (e.g. new auto_resolved)', () => {
    const local = makeCard()
    const server = makeCard({ auto_resolved: ['Scope'] })
    const changes = reconcilePendingAsks([server], { s1: { pendingAsk: local } })
    expect(changes.s1).toBe(server)
  })

  it('replaces (not clears) a DIFFERENT stale local pending card when the snapshot brings a new one', () => {
    const stale = makeCard({ card_id: 'ask_old' })
    const fresh = makeCard({ card_id: 'ask_new' })
    const changes = reconcilePendingAsks([fresh], { s1: { pendingAsk: stale } })
    // One write only — the hydration; the clear pass must not null it back out.
    expect(changes).toEqual({ s1: fresh })
  })
})

describe('reconcilePendingAsks — card resolved while disconnected', () => {
  it('clears a locally pending card the snapshot no longer lists', () => {
    const changes = reconcilePendingAsks([], { s1: { pendingAsk: makeCard() } })
    expect(changes).toEqual({ s1: null })
  })

  it('clears only the resolved session, leaving other sessions untouched', () => {
    const kept = makeCard({ card_id: 'ask_2', session_id: 's2' })
    const changes = reconcilePendingAsks([kept], {
      s1: { pendingAsk: makeCard() },
      s2: { pendingAsk: kept },
      s3: {},
    })
    expect(changes.s1).toBeNull()
    expect(changes.s2).toBe(kept)
    expect('s3' in changes).toBe(false)
  })

  it('never clears a locally TERMINAL card (the collapsed record stays in the thread)', () => {
    const answered = makeCard({ status: 'answered' })
    const changes = reconcilePendingAsks([], { s1: { pendingAsk: answered } })
    expect(changes).toEqual({})
  })
})

describe('reconcilePendingAsks — duplicate hydration is idempotent', () => {
  it('applying the same snapshot twice yields the identical state', () => {
    const snapshot = [makeCard(), makeCard({ card_id: 'ask_2', session_id: 's2' })]
    const start: Record<string, PendingAskBucket> = {
      s1: {},
      s3: { pendingAsk: makeCard({ card_id: 'ask_gone', session_id: 's3' }) },
    }
    const once = apply(start, reconcilePendingAsks(snapshot, start))
    const twice = apply(once, reconcilePendingAsks(snapshot, once))
    expect(twice).toEqual(once)
  })

  it('a second reconcile of the same snapshot clears nothing', () => {
    const snapshot = [makeCard()]
    const afterFirst = apply({}, reconcilePendingAsks(snapshot, {}))
    const second = reconcilePendingAsks(snapshot, afterFirst)
    expect(Object.values(second)).not.toContain(null)
  })
})

describe('reconcilePendingAsks — race with a live frame', () => {
  it('does NOT resurrect a card a live terminal frame already resolved (answered)', () => {
    // The live ask_user_question terminal frame landed before the (stale)
    // snapshot: answered/cancelled are terminal server-side, so a snapshot
    // still listing the card as pending can never be fresher.
    const resolved = makeCard({ status: 'answered' })
    const staleSnapshotCopy = makeCard() // same card_id, still 'pending'
    const changes = reconcilePendingAsks([staleSnapshotCopy], { s1: { pendingAsk: resolved } })
    expect(changes).toEqual({})
  })

  it('does NOT resurrect a cancelled card either', () => {
    const cancelled = makeCard({ status: 'cancelled' })
    const changes = reconcilePendingAsks([makeCard()], { s1: { pendingAsk: cancelled } })
    expect(changes).toEqual({})
  })

  it('a terminal record for a DIFFERENT card does not block hydrating a genuinely new one', () => {
    const oldRecord = makeCard({ card_id: 'ask_old', status: 'answered' })
    const fresh = makeCard({ card_id: 'ask_new' })
    const changes = reconcilePendingAsks([fresh], { s1: { pendingAsk: oldRecord } })
    expect(changes).toEqual({ s1: fresh })
  })
})
