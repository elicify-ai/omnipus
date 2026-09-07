// GoalThreadTailCards.test.tsx — ADR-081 D5/D9 (work-first goal flow, test
// 25): the record card renders from the goal's ACTIVE pill once it carries
// a criteria breakdown — no more `queued` state to key off, no button row,
// no "Compiling your goal…" indicator (the compile step it bridged no
// longer exists), no GoalAmendmentDiff re-export (dormant, deleted). A
// G-5-style `waiting_on_user` pause, and a freshly-activated goal whose
// record is still empty (D1's transient active+empty-record window), both
// render no card — only an authored record does.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import { GoalThreadTailCards } from './GoalThreadTailCards'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

function makeGoal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'write the launch post',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 0,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

const oneCriterion: NonNullable<GoalStatusFrame['criteria']> = [
  {
    kind: 'prose',
    judgment: 'boolean',
    text: 'the post names every shipped feature',
    author: { kind: 'agent', id: 'mia' },
    status: 'pending',
  },
]

describe('GoalThreadTailCards', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
  })

  it('renders nothing with no pills', () => {
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the record card for an active pill that carries a criteria breakdown', () => {
    useChatStore.setState({
      goalPills: {
        g1: makeGoal({
          goal_id: 'g1',
          criteria: [
            ...oneCriterion,
            {
              kind: 'check',
              judgment: 'boolean',
              text: 'the site builds',
              check: { command: 'npm run build', expected_exit_code: 0 },
              author: { kind: 'agent', id: 'mia' },
              status: 'pending',
            },
          ],
        }),
      },
    })
    render(<GoalThreadTailCards />)
    expect(screen.getByTestId('goal-thread-tail-cards')).toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('write the launch post')
  })

  // ADR-081 D1: instant activation writes an `active` frame with an EMPTY
  // record (`GoalCriteriaJSON` starts empty, filled in by the agent's first
  // move) — that transient window must render no card.
  it('does NOT render a card for an active goal whose record is still empty (D1 transient window)', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1' }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  // US-6 S3 negative (R2-03, preserved): an ACTIVE goal paused
  // waiting_on_user is not a registered-record display case.
  it('does NOT render a card for a waiting_on_user pause', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'waiting_on_user', round: 3, criteria: oneCriterion }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  // ADR-081 D9: `queued` is never emitted anymore, but the wire-enum value
  // survives in the generated type — a stale/legacy queued pill (however it
  // got there) must still render no card and no button row.
  it('does NOT render a card for a queued pill (retired state, defensive)', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'queued', criteria: oneCriterion }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  it('does NOT render a card for a terminal state (done/failed/cleared) even with criteria', () => {
    for (const state of ['done', 'failed', 'cleared'] as const) {
      useChatStore.setState({
        goalPills: { g1: makeGoal({ goal_id: 'g1', state, criteria: oneCriterion }) },
      })
      const { container, unmount } = render(<GoalThreadTailCards />)
      expect(container).toBeEmptyDOMElement()
      unmount()
    }
  })

  it('renders one card per goal_id when multiple active goals each carry a record', () => {
    useChatStore.setState({
      goalPills: {
        g1: makeGoal({ goal_id: 'g1', condition: 'ship the release', criteria: oneCriterion }),
        g2: makeGoal({ goal_id: 'g2', condition: 'fix bug #42', criteria: oneCriterion }),
      },
    })
    render(<GoalThreadTailCards />)
    expect(screen.getAllByTestId('goal-echo-card')).toHaveLength(2)
  })

  // ── Regression coverage: ADR-081 code-review round 1, Finding 1 (HIGH) ──
  //
  // Full pipeline (handleFrame → store merge → this component), not a
  // hand-set fixture — proves the field-preserving merge in chat.ts's
  // `case 'goal_status'` keeps the record card mounted across the exact
  // masked sequence the reviewer named: a criteria-carrying set_goal
  // emission, immediately followed by a routine criteria-less progress
  // frame from the goal loop.
  describe('across the live handleFrame pipeline (ADR-081 code-review round 1, Finding 1)', () => {
    const SID = 's1'

    beforeEach(() => {
      act(() => {
        useSessionStore.setState({ activeSessionId: SID })
      })
    })

    it('stays rendered with the record after a routine criteria-less active frame follows a criteria frame', () => {
      act(() => {
        useChatStore
          .getState()
          .handleFrame(makeGoal({ goal_id: 'g1', state: 'active', round: 1, criteria: oneCriterion }))
      })
      const { rerender } = render(<GoalThreadTailCards />)
      expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()

      act(() => {
        // Routine end-of-turn push — no criteria/dod/definition, matching
        // the real engine's non-set_goal goal_status emissions.
        useChatStore.getState().handleFrame(makeGoal({ goal_id: 'g1', state: 'active', round: 2 }))
      })
      rerender(<GoalThreadTailCards />)
      expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()
      expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('write the launch post')
      // The record itself (the criteria breakdown) survived the routine
      // frame — the accordion trigger only renders when criteria.length>0.
      expect(screen.getByTestId('goal-echo-criteria-trigger')).toHaveTextContent('Done when · 1 criterion')
    })

    it('the card disappears once a done frame follows a criteria frame (terminal wins wholesale)', () => {
      act(() => {
        useChatStore
          .getState()
          .handleFrame(makeGoal({ goal_id: 'g1', state: 'active', round: 1, criteria: oneCriterion }))
      })
      const { rerender } = render(<GoalThreadTailCards />)
      expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()

      act(() => {
        useChatStore.getState().handleFrame(makeGoal({ goal_id: 'g1', state: 'done', round: 5 }))
      })
      rerender(<GoalThreadTailCards />)
      expect(screen.queryByTestId('goal-echo-card')).not.toBeInTheDocument()
    })

    it('a fresh criteria-frame (steering update) shows the new record, not the old one', () => {
      const updatedCriteria: NonNullable<GoalStatusFrame['criteria']> = [
        {
          kind: 'prose',
          judgment: 'boolean',
          text: 'the launch post links the changelog',
          author: { kind: 'agent', id: 'mia' },
          status: 'pending',
        },
      ]
      act(() => {
        useChatStore
          .getState()
          .handleFrame(makeGoal({ goal_id: 'g1', state: 'active', round: 1, criteria: oneCriterion }))
      })
      const { rerender } = render(<GoalThreadTailCards />)
      fireEvent.click(screen.getByTestId('goal-echo-criteria-trigger'))
      expect(screen.getByText('the post names every shipped feature')).toBeInTheDocument()

      act(() => {
        useChatStore
          .getState()
          .handleFrame(makeGoal({ goal_id: 'g1', state: 'active', round: 2, criteria: updatedCriteria }))
      })
      rerender(<GoalThreadTailCards />)
      expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()
      // The accordion's local `open` state is preserved across the rerender
      // (same React key === same component instance) — no second click
      // needed; it is still expanded from above.
      expect(screen.getByText('the launch post links the changelog')).toBeInTheDocument()
      expect(screen.queryByText('the post names every shipped feature')).not.toBeInTheDocument()
      expect(screen.getByText('the launch post links the changelog')).toBeInTheDocument()
    })
  })
})
