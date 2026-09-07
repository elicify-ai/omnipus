// GoalThreadTailCards.test.tsx — ADR-053 FE-8 / US-3; ADR-074 D5.2 /
// judgment-first FR-011 (US-6 S1/S3, test 19 component half): the `queued`
// pill renders the echo card with real itemization from the frame's
// `criteria` breakdown, and a G-5 `waiting_on_user` pause on an ACTIVE goal
// never renders the confirm card (R2-03's kept negative).

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { GoalThreadTailCards } from './GoalThreadTailCards'
import { useChatStore } from '@/store/chat'
import type {
  GoalStatusFrame,
  AskUserQuestionCard as AskUserCard,
} from '@/lib/api/generated/asyncapi-types'

// ADR-078 D1: Amend pre-fills the composer via AssistantUI's
// `useComposerRuntime().setText(...)` — the same mechanism `useSlashMenu.ts`
// already uses (e.g. `composerRuntime.setText('/skills')`). Mock the whole
// module (this component only touches `useComposerRuntime`).
const mockSetText = vi.hoisted(() => vi.fn())
vi.mock('@assistant-ui/react', () => ({
  useComposerRuntime: () => ({ setText: mockSetText }),
}))

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
    state: 'queued',
    ...overrides,
  }
}

function makeAsk(status: AskUserCard['status']): AskUserCard {
  return {
    card_id: 'ask_1',
    session_id: 's1',
    agent_id: 'mia',
    status,
    created_at: '2026-09-06T12:00:00Z',
    questions: [{ header: 'Scope', question: 'What should the goal cover?', options: [] }],
  }
}

describe('GoalThreadTailCards', () => {
  beforeEach(() => {
    mockSetText.mockClear()
    useChatStore.setState({
      goalPills: {},
      sendMessage: vi.fn(),
      pendingAsk: null,
      isStreaming: false,
    })
  })

  it('renders nothing with no pills', () => {
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the echo card with real itemization for a queued pill carrying criteria', () => {
    useChatStore.setState({
      goalPills: {
        _default: makeGoal({
          criteria: [
            {
              kind: 'prose',
              judgment: 'boolean',
              text: 'the post names every shipped feature',
              author: { kind: 'agent', id: 'mia' },
              status: 'pending',
            },
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
    // Criteria render through the shared CriteriaBreakdown (D5.4), behind
    // GoalEchoCard's collapsed-by-default accordion (redesign, operator
    // report 2026-09-07) — expand it first.
    fireEvent.click(screen.getByTestId('goal-echo-criteria-trigger'))
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByText('verifies via:')).toBeInTheDocument()
    expect(screen.getByText('npm run build -> exit 0')).toBeInTheDocument()
  })

  // ADR-078 D1: Confirm sends the bare confirm token over the existing chat
  // path — no new wire type, no new store action.
  it('clicking Confirm sends the bare chat message "confirm"', () => {
    const sendSpy = vi.fn()
    useChatStore.setState({
      goalPills: { _default: makeGoal() },
      sendMessage: sendSpy,
    })
    render(<GoalThreadTailCards />)
    fireEvent.click(screen.getByTestId('goal-echo-confirm'))
    expect(sendSpy).toHaveBeenCalledTimes(1)
    expect(sendSpy).toHaveBeenCalledWith('confirm')
    expect(mockSetText).not.toHaveBeenCalled()
  })

  // ADR-078 D1: Cancel sends `/goal clear`, the slash command the backend
  // router (`clearGoal`) already handles for a pending-but-unconfirmed goal.
  it('clicking Cancel sends the chat message "/goal clear"', () => {
    const sendSpy = vi.fn()
    useChatStore.setState({
      goalPills: { _default: makeGoal() },
      sendMessage: sendSpy,
    })
    render(<GoalThreadTailCards />)
    fireEvent.click(screen.getByTestId('goal-echo-cancel'))
    expect(sendSpy).toHaveBeenCalledTimes(1)
    expect(sendSpy).toHaveBeenCalledWith('/goal clear')
    expect(mockSetText).not.toHaveBeenCalled()
  })

  // ADR-078 D1: Amend sends nothing — it pre-fills the composer via the
  // AssistantUI composer runtime so the user restates the goal themselves
  // (restatement stays an explicit user action; a routine click never
  // silently mutates goal state).
  it('clicking Amend pre-fills the composer with "/goal " and does NOT send a chat message', () => {
    const sendSpy = vi.fn()
    useChatStore.setState({
      goalPills: { _default: makeGoal() },
      sendMessage: sendSpy,
    })
    render(<GoalThreadTailCards />)
    fireEvent.click(screen.getByTestId('goal-echo-amend'))
    expect(mockSetText).toHaveBeenCalledTimes(1)
    expect(mockSetText).toHaveBeenCalledWith('/goal ')
    expect(sendSpy).not.toHaveBeenCalled()
  })

  // US-6 S3 negative (R2-03): an ACTIVE goal paused waiting_on_user is NOT a
  // pending-confirm state — the confirm card must not render.
  it('does NOT render the confirm card for a waiting_on_user pause', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'waiting_on_user', round: 3 }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  it('does NOT render the confirm card for an active goal', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'active', round: 1 }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })
})

// "Compiling your goal…" (nice-to-have, operator report 2026-09-07): a
// resumed goal-compile step after a clarify AskUserQuestion answer runs
// silently — no streamed tokens, no isStreaming — until the queued
// goal_status frame lands. Tracked locally off the pendingAsk pending ->
// answered edge; see GoalThreadTailCards.tsx's doc comment for the full
// rationale.
describe('GoalThreadTailCards — "Compiling your goal…" indicator', () => {
  beforeEach(() => {
    useChatStore.setState({
      goalPills: {},
      sendMessage: vi.fn(),
      pendingAsk: null,
      isStreaming: false,
    })
  })

  it('stays absent while a question is merely pending (not yet answered)', () => {
    useChatStore.setState({ pendingAsk: makeAsk('pending') })
    render(<GoalThreadTailCards />)
    expect(screen.queryByTestId('goal-compiling-indicator')).not.toBeInTheDocument()
  })

  it('arms on the pending -> answered edge, and hands off to the real card once a queued pill lands', () => {
    useChatStore.setState({ pendingAsk: makeAsk('pending') })
    const { rerender } = render(<GoalThreadTailCards />)
    expect(screen.queryByTestId('goal-compiling-indicator')).not.toBeInTheDocument()

    useChatStore.setState({ pendingAsk: makeAsk('answered') })
    rerender(<GoalThreadTailCards />)
    expect(screen.getByTestId('goal-compiling-indicator')).toHaveTextContent('Compiling your goal…')
    expect(screen.queryByTestId('goal-echo-card')).not.toBeInTheDocument()

    // The compiled goal lands — the real summary card takes over and the
    // silent-wait indicator disappears.
    useChatStore.setState({ goalPills: { _default: makeGoal() } })
    rerender(<GoalThreadTailCards />)
    expect(screen.queryByTestId('goal-compiling-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()
  })

  it('does NOT arm on a pending -> cancelled edge (no compile follows a cancelled question)', () => {
    useChatStore.setState({ pendingAsk: makeAsk('pending') })
    const { rerender } = render(<GoalThreadTailCards />)
    useChatStore.setState({ pendingAsk: makeAsk('cancelled') })
    rerender(<GoalThreadTailCards />)
    expect(screen.queryByTestId('goal-compiling-indicator')).not.toBeInTheDocument()
  })

  it('clears once an ordinary streamed reply begins, even with no queued goal', () => {
    useChatStore.setState({ pendingAsk: makeAsk('pending') })
    const { rerender } = render(<GoalThreadTailCards />)
    useChatStore.setState({ pendingAsk: makeAsk('answered') })
    rerender(<GoalThreadTailCards />)
    expect(screen.getByTestId('goal-compiling-indicator')).toBeInTheDocument()

    useChatStore.setState({ isStreaming: true })
    rerender(<GoalThreadTailCards />)
    expect(screen.queryByTestId('goal-compiling-indicator')).not.toBeInTheDocument()
  })

  it('gives up after a generous timeout if no goal ever compiles', () => {
    vi.useFakeTimers()
    try {
      useChatStore.setState({ pendingAsk: makeAsk('pending') })
      const { rerender } = render(<GoalThreadTailCards />)
      useChatStore.setState({ pendingAsk: makeAsk('answered') })
      rerender(<GoalThreadTailCards />)
      expect(screen.getByTestId('goal-compiling-indicator')).toBeInTheDocument()

      act(() => {
        vi.advanceTimersByTime(90_000)
      })
      expect(screen.queryByTestId('goal-compiling-indicator')).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })
})
