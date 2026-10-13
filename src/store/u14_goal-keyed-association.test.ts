// RED pack — WC-RESUME RED·U13/U14, unit U14 (FR-039; DEL-F41).
//
// Spec source: docs/internal/specs/session-core-spec.md
//   FR-039 — "Thinking/error indicators MUST join their own producing
//            run/turn/message goal_id to exact keyed merged criteria;
//            unknown neutral."
//   DEL-F41 — old branch: "Missing/empty legacy `goal_id` goes into
//            `_default`; a keyed frame additionally deletes that
//            compatibility entry."  Replacement: "Explicit goal_id keys; no
//            invented _default ID for unkeyed old frame."
//   C-GOAL UI row — "Exact goalPills[goal_id] join with merged criteria.
//            Matching empty active goal may show setup; different/nonempty/
//            unknown uses ordinary/neutral state."
//   BDD-12.5   — "Run A/message a→G1 … B/b→G2 … c unknown. … c neutral. …
//            no goalStatus/_default/latest-map indicator choice."
//
// Oracle provenance: the spec's replacement column. A `goal_status` frame
// carrying NO goal_id is the UNKNOWN association — it must NOT be filed under
// an invented `'_default'` key (that compat key is exactly what DEL-F41
// removes), because an unknown goal can never be matched to exact keyed
// criteria. A frame carrying a real goal_id is filed under EXACTLY that key.
// The expected values below come from the spec, NOT from the current
// implementation (which files every unkeyed frame under `'_default'` — see
// the existing `chat.goal-status-frame.test.ts` ADR-088 D5 block that this
// pack's unit will have to update).
//
// NOTE for GREEN: this RED pack's assertions deliberately contradict the
// ADR-088 D5 `_default` eviction block in `src/store/chat.goal-status-frame
// .test.ts` (its "does not evict '_default' when the incoming frame is itself
// unkeyed" case). DEL-F41 removes the `'_default'` compat key, so that
// compatibility-only case is part of the removal — it must be updated, not
// preserved.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

const SID = 'u14-goal-keyed-test-sid'

function makeFrame(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: SID,
    condition: 'ship the release',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

function pills() {
  return useChatStore.getState().sessionsById[SID]?.goalPills ?? {}
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ sessionsById: {}, goalStatus: null, loopStatus: null })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  })
})

describe('U14/DEL-F41 — an unkeyed goal_status frame invents no `_default` pill', () => {
  it('files an empty-goal_id frame under no key at all (unknown is neutral, not `_default`)', () => {
    // Spec (DEL-F41): "no invented _default ID for unkeyed old frame."
    // The frame carries no goal_id — an UNKNOWN association.
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ state: 'active' }))
    })
    // No `_default` compatibility entry may be invented…
    expect(pills()['_default']).toBeUndefined()
    // …and no pill at all is filed for an unknown goal.
    expect(Object.keys(pills())).toEqual([])
  })

  it('does not create a `_default` pill on repeat unkeyed frames either', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ state: 'active', round: 1 }))
      useChatStore.getState().handleFrame(makeFrame({ state: 'judging', round: 2 }))
    })
    expect(pills()['_default']).toBeUndefined()
    expect(Object.keys(pills())).toEqual([])
  })
})

describe('U14/DEL-F41 — a keyed goal_status frame files under its exact goal_id', () => {
  it('files a frame with a real goal_id under exactly that key (canonical positive control)', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ goal_id: 'goal-g1', state: 'active', round: 1 }))
    })
    expect(Object.keys(pills())).toEqual(['goal-g1'])
    expect(pills()['goal-g1']?.round).toBe(1)
  })

  it('keeps two live goals isolated under their own keys — a later key never relabels an earlier goal', () => {
    // BDD-12.5: G1 and G2 are distinct; a frame for G2 must never relabel G1.
    const g1Criteria: NonNullable<GoalStatusFrame['criteria']> = [
      { kind: 'prose', judgment: 'boolean', text: 'release notes published', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
    ]
    act(() => {
      useChatStore
        .getState()
        .handleFrame(makeFrame({ goal_id: 'goal-g1', state: 'active', round: 1, criteria: g1Criteria }))
      // A DIFFERENT goal arrives, keyed G2, no criteria.
      useChatStore.getState().handleFrame(makeFrame({ goal_id: 'goal-g2', state: 'active', round: 1 }))
    })
    const map = pills()
    expect(map['goal-g1']?.criteria).toEqual(g1Criteria)
    expect(map['goal-g2']?.criteria).toBeUndefined()
    // The exact keyed join stays exact — no cross-goal relabelling.
    expect(map['goal-g1']?.goal_id).toBe('goal-g1')
    expect(map['goal-g2']?.goal_id).toBe('goal-g2')
  })
})
