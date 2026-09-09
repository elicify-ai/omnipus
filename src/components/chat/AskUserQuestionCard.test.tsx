// AskUserQuestionCard.test.tsx — askuserquestion-tool-spec v3 test 9
// (component half): tabs/advance/badge/underline/countdown/collapsed/
// composer-lock note/hostile-markdown inertness, plus the client-fired
// 5-minute grace auto-submit (US-3 S3).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { AskUserQuestionCard, AskUserQuestionThreadTail, ASK_GRACE_MS } from './AskUserQuestionCard'
import { useChatStore } from '@/store/chat'
import type { AskUserQuestionCard as AskUserCard, AskUserAnswerFrame } from '@/lib/api/generated/asyncapi-types'

function makeCard(overrides: Partial<AskUserCard> = {}): AskUserCard {
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
        options: [
          { label: 'Only unanswered', description: 'The 14 currently waiting.' },
          { label: 'All customer email' },
        ],
        recommended: 'Only unanswered',
      },
      {
        header: 'Sending',
        question: 'Draft or send directly?',
        options: [{ label: 'Draft only' }, { label: 'Send directly' }],
      },
    ],
    ...overrides,
  }
}

describe('AskUserQuestionCard — pending card', () => {
  let sendSpy: ReturnType<typeof vi.fn<(answer: Omit<AskUserAnswerFrame, 'type'>) => void>>

  beforeEach(() => {
    sendSpy = vi.fn<(answer: Omit<AskUserAnswerFrame, 'type'>) => void>()
    useChatStore.setState({ sendAskUserAnswer: sendSpy })
  })

  it('shows one question at a time via tabs, with an n/M counter', () => {
    render(<AskUserQuestionCard card={makeCard()} />)
    expect(screen.getByText('Which emails should this goal cover?')).toBeInTheDocument()
    expect(screen.queryByText('Draft or send directly?')).toBeNull()
    expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('0 / 2 answered')

    fireEvent.click(screen.getByTestId('ask-user-tab-1'))
    expect(screen.getByText('Draft or send directly?')).toBeInTheDocument()
    expect(screen.queryByText('Which emails should this goal cover?')).toBeNull()
  })

  it('lists the recommended option FIRST with a badge, never pre-selected', () => {
    render(<AskUserQuestionCard card={makeCard()} />)
    const options = screen.getAllByTestId('ask-user-option')
    expect(options[0]).toHaveTextContent('Only unanswered')
    expect(screen.getByTestId('ask-user-recommended-badge')).toBeInTheDocument()
    for (const o of options) {
      expect(o).toHaveAttribute('aria-pressed', 'false')
    }
    expect(screen.getByTestId('ask-user-submit')).toBeDisabled()
  })

  it('selecting answers marks the tab, auto-advances, and enables Answer only when all are answered', () => {
    vi.useFakeTimers()
    try {
      render(<AskUserQuestionCard card={makeCard()} />)
      fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
      act(() => {
        vi.advanceTimersByTime(300)
      })
      // Auto-advanced to the second (unanswered) question.
      expect(screen.getByText('Draft or send directly?')).toBeInTheDocument()
      expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('1 / 2 answered')
      expect(screen.getByTestId('ask-user-submit')).toBeDisabled()

      fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
      act(() => {
        vi.advanceTimersByTime(300)
      })
      expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('2 / 2 answered')
      expect(screen.getByTestId('ask-user-submit')).toBeEnabled()

      fireEvent.click(screen.getByTestId('ask-user-submit'))
      expect(sendSpy).toHaveBeenCalledWith({
        card_id: 'ask_1',
        session_id: 's1',
        answers: [
          { header: 'Scope', selected: ['Only unanswered'] },
          { header: 'Sending', selected: ['Draft only'] },
        ],
      })
    } finally {
      vi.useRealTimers()
    }
  })

  it('free text is an underline input; typing deselects, re-selecting drops the text (EC-3)', () => {
    render(<AskUserQuestionCard card={makeCard()} />)
    fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
    const input = screen.getByTestId('ask-user-free-text')
    fireEvent.change(input, { target: { value: 'my own scope' } })
    // Typing deselected the option.
    expect(screen.getAllByTestId('ask-user-option')[0]).toHaveAttribute('aria-pressed', 'false')
    // Re-selecting drops the free text.
    fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
    expect(screen.getByTestId('ask-user-free-text')).toHaveValue('')
  })

  it('Cancel is always present and sends the cancel frame', () => {
    render(<AskUserQuestionCard card={makeCard()} />)
    fireEvent.click(screen.getByTestId('ask-user-cancel'))
    expect(sendSpy).toHaveBeenCalledWith({ card_id: 'ask_1', session_id: 's1', cancel: true })
  })

  it('renders the countdown on a default-safe question', () => {
    const card = makeCard({
      default_safe_at: '2026-09-06T12:30:00Z',
      questions: [
        {
          header: 'Sending',
          question: 'Draft or send directly?',
          options: [{ label: 'Draft only' }, { label: 'Send directly' }],
          recommended: 'Draft only',
          default_safe: true,
        },
      ],
    })
    render(<AskUserQuestionCard card={card} />)
    const countdown = screen.getByTestId('ask-user-countdown')
    expect(countdown).toHaveTextContent(/No answer in \d+:\d{2}/)
    expect(countdown).toHaveTextContent('"Draft only" is chosen automatically')
  })

  it('marks a server-auto-resolved question answered and submits it auto_default on grace expiry', () => {
    vi.useFakeTimers()
    try {
      const card = makeCard({
        auto_resolved: ['Sending'],
        questions: [
          {
            header: 'Scope',
            question: 'Which emails?',
            options: [{ label: 'Only unanswered' }, { label: 'All' }],
            recommended: 'Only unanswered',
          },
          {
            header: 'Sending',
            question: 'Draft or send?',
            options: [{ label: 'Draft only' }, { label: 'Send directly' }],
            recommended: 'Draft only',
            default_safe: true,
          },
        ],
      })
      render(<AskUserQuestionCard card={card} />)
      // One human answer on Scope; Sending is auto-resolved → all answered.
      fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
      expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('2 / 2 answered')

      // Client-fired grace auto-submit after 5 minutes without interaction.
      act(() => {
        vi.advanceTimersByTime(ASK_GRACE_MS + 6000)
      })
      expect(sendSpy).toHaveBeenCalledTimes(1)
      expect(sendSpy).toHaveBeenCalledWith({
        card_id: 'ask_1',
        session_id: 's1',
        answers: [
          { header: 'Scope', selected: ['Only unanswered'] },
          { header: 'Sending', selected: ['Draft only'], auto_default: true },
        ],
      })
    } finally {
      vi.useRealTimers()
    }
  })

  // Stale-closure regression: the grace interval used to be keyed on
  // `allAnswered` and captured that render's drafts — once all questions were
  // first answered, a LATER edit changed the drafts but not the interval's
  // closure, so the grace timer submitted the discarded pre-edit answer.
  it('grace auto-submit sends the EDITED answer when one is changed after all were answered', () => {
    vi.useFakeTimers()
    try {
      render(<AskUserQuestionCard card={makeCard()} />)
      // Answer both questions (auto-advance between them).
      fireEvent.click(screen.getAllByTestId('ask-user-option')[0]) // Scope: Only unanswered
      act(() => {
        vi.advanceTimersByTime(300)
      })
      fireEvent.click(screen.getAllByTestId('ask-user-option')[0]) // Sending: Draft only
      act(() => {
        vi.advanceTimersByTime(300)
      })
      expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('2 / 2 answered')

      // EDIT: go back to the first question and pick the OTHER option.
      fireEvent.click(screen.getByTestId('ask-user-tab-0'))
      fireEvent.click(screen.getAllByTestId('ask-user-option')[1]) // All customer email

      // Let the grace window expire with no further interaction.
      act(() => {
        vi.advanceTimersByTime(ASK_GRACE_MS + 6000)
      })
      expect(sendSpy).toHaveBeenCalledTimes(1)
      expect(sendSpy).toHaveBeenCalledWith({
        card_id: 'ask_1',
        session_id: 's1',
        answers: [
          { header: 'Scope', selected: ['All customer email'] },
          { header: 'Sending', selected: ['Draft only'] },
        ],
      })
    } finally {
      vi.useRealTimers()
    }
  })

  // Same stale-closure family, free-text flavor: typing a free-text answer
  // over an earlier option selection must be what the grace timer submits.
  it('grace auto-submit sends free text typed AFTER all questions were answered', () => {
    vi.useFakeTimers()
    try {
      render(<AskUserQuestionCard card={makeCard()} />)
      fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
      act(() => {
        vi.advanceTimersByTime(300)
      })
      fireEvent.click(screen.getAllByTestId('ask-user-option')[0])
      act(() => {
        vi.advanceTimersByTime(300)
      })

      // EDIT the second question's answer into free text.
      fireEvent.click(screen.getByTestId('ask-user-tab-1'))
      fireEvent.change(screen.getByTestId('ask-user-free-text'), {
        target: { value: 'draft, but flag urgent ones' },
      })

      act(() => {
        vi.advanceTimersByTime(ASK_GRACE_MS + 6000)
      })
      expect(sendSpy).toHaveBeenCalledTimes(1)
      expect(sendSpy).toHaveBeenCalledWith({
        card_id: 'ask_1',
        session_id: 's1',
        answers: [
          { header: 'Scope', selected: ['Only unanswered'] },
          { header: 'Sending', free_text: 'draft, but flag urgent ones' },
        ],
      })
    } finally {
      vi.useRealTimers()
    }
  })

  it('renders hostile markdown context INERT through the sanitized chat pipeline', () => {
    const hostile =
      'Look: <img src=x onerror="window.__pwned=1"> and <script>window.__pwned=2</script> **bold ok**'
    const card = makeCard({
      questions: [
        {
          header: 'Scope',
          question: 'Q?',
          options: [{ label: 'A' }, { label: 'B' }],
          context: hostile,
        },
      ],
    })
    render(<AskUserQuestionCard card={card} />)
    const ctx = screen.getByTestId('ask-user-context')
    // Raw HTML never materializes as elements — no img, no script.
    expect(ctx.querySelector('img')).toBeNull()
    expect(ctx.querySelector('script')).toBeNull()
    expect((window as unknown as Record<string, unknown>).__pwned).toBeUndefined()
    // Legitimate markdown still renders.
    expect(ctx.textContent).toContain('bold ok')
  })
})

describe('AskUserQuestionCard — collapsed record', () => {
  beforeEach(() => {
    useChatStore.setState({ sendAskUserAnswer: vi.fn() })
  })

  it('renders the answered record with auto-default markers from the registry record', () => {
    const card = makeCard({
      status: 'answered',
      answers: [
        {
          header: 'Scope',
          question: 'Which emails should this goal cover?',
          selected: ['Only unanswered'],
          auto_default: false,
        },
        {
          header: 'Sending',
          question: 'Draft or send directly?',
          selected: ['Draft only'],
          auto_default: true,
        },
      ],
    })
    render(<AskUserQuestionCard card={card} />)
    const rows = screen.getAllByTestId('ask-user-record-row')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Which emails should this goal cover?')
    expect(rows[0]).toHaveTextContent('Only unanswered')
    // Auto-defaulted answers are marked so they're never mistaken for a
    // human choice; human answers are not.
    expect(rows[0].querySelector('[data-testid="ask-user-auto-marker"]')).toBeNull()
    expect(rows[1].querySelector('[data-testid="ask-user-auto-marker"]')).not.toBeNull()
    // The question zone is gone.
    expect(screen.queryByTestId('ask-user-question-card')).toBeNull()
  })

  it('renders a cancelled record with no answers', () => {
    render(<AskUserQuestionCard card={makeCard({ status: 'cancelled' })} />)
    expect(screen.getByTestId('ask-user-collapsed')).toHaveTextContent('Cancelled')
    expect(screen.queryAllByTestId('ask-user-record-row')).toHaveLength(0)
  })
})

describe('AskUserQuestionThreadTail — composer lock note', () => {
  afterEach(() => {
    useChatStore.setState({ pendingAsk: null })
  })

  it('renders nothing without a card', () => {
    useChatStore.setState({ pendingAsk: null, sendAskUserAnswer: vi.fn() })
    const { container } = render(<AskUserQuestionThreadTail />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the card + locked-composer note while pending, and disappears entirely once terminal', () => {
    useChatStore.setState({ pendingAsk: makeCard(), sendAskUserAnswer: vi.fn() })
    const { rerender, container } = render(<AskUserQuestionThreadTail />)
    expect(screen.getByTestId('ask-user-question-card')).toBeInTheDocument()
    expect(screen.getByTestId('ask-user-composer-note')).toHaveTextContent(/locked while questions are pending/)

    // Once answered/cancelled the docked card leaves NO lingering summary on the
    // page (operator directive 2026-09-06): ThreadTail renders nothing.
    useChatStore.setState({ pendingAsk: makeCard({ status: 'cancelled' }) })
    rerender(<AskUserQuestionThreadTail />)
    expect(screen.queryByTestId('ask-user-composer-note')).toBeNull()
    expect(screen.queryByTestId('ask-user-collapsed')).toBeNull()
    expect(screen.queryByTestId('ask-user-question-card')).toBeNull()
    expect(container).toBeEmptyDOMElement()
  })
})
