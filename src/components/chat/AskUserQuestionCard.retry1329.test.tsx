// SFH F2/F3 (#1329/#1343): a failed action is recoverable without losing
// drafts; a promoted card never inherits another card's choices or latch.
// Real card, catalogued controls and stores; only socket.send is doubled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { AskUserAnswerFrame, AskUserQuestionCard as AskCard } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { ASK_GRACE_MS, AskUserQuestionThreadTail } from './AskUserQuestionCard'

const CARD: AskCard = {
  card_id: 'pending-a', session_id: 'retry-session', agent_id: 'omnipus',
  status: 'pending', created_at: '2026-10-16T12:00:00Z',
  questions: [
    { header: 'Scope', question: 'Which area?', options: [{ label: 'Sales' }, { label: 'Support' }] },
    { header: 'Apply', question: 'How should we continue?', options: [{ label: 'Draft' }, { label: 'Send' }] },
  ],
}
const SINGLE: AskCard = { ...CARD, questions: [CARD.questions[0]] }
let socket: WsConnection
let send: ReturnType<typeof vi.fn<WsConnection['send']>>

beforeEach(() => {
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(send)
  useConnectionStore.setState({ connection: socket, isConnected: true, connectionError: null })
})
afterEach(() => {
  cleanup()
  socket.disconnect()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.useRealTimers()
  vi.restoreAllMocks()
})
function show(card: AskCard = CARD) {
  act(() => { useChatStore.setState({ pendingAsk: card }) })
  return render(<AskUserQuestionThreadTail />)
}
function replace(card: AskCard) {
  act(() => { useChatStore.setState({ pendingAsk: card }) })
}
function answerWithFreeText(value: string) {
  fireEvent.change(screen.getByTestId('ask-user-free-text'), { target: { value } })
}
function answerFrame(card: AskCard, text: string): AskUserAnswerFrame {
  return { type: 'ask_user_answer', card_id: card.card_id, session_id: card.session_id,
    answers: [{ header: 'Scope', free_text: text }] }
}

describe('F2 question-card transport recovery', () => {
  it('keeps a failed answer draft, announces the failure inline and retries on the same card', () => {
    send.mockReturnValueOnce(false)
    show(SINGLE)
    answerWithFreeText('Keep my exact answer')
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect.soft(screen.queryByRole('alert')).toHaveTextContent('Failed to send your answers — connection dropped. Reconnect and try again.')
    expect(screen.getByTestId('ask-user-free-text')).toHaveValue('Keep my exact answer')
    expect(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answerFrame(SINGLE, 'Keep my exact answer')], [answerFrame(SINGLE, 'Keep my exact answer')]])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('allows Cancel after a failed cancellation without clearing the original draft', () => {
    send.mockReturnValueOnce(false)
    show(SINGLE)
    answerWithFreeText('Keep this until cancellation succeeds')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect.soft(screen.queryByRole('alert')).toHaveTextContent('Failed to send your answers — connection dropped. Reconnect and try again.')
    expect(screen.getByTestId('ask-user-free-text')).toHaveValue('Keep this until cancellation succeeds')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    const cancel: AskUserAnswerFrame = { type: 'ask_user_answer', card_id: SINGLE.card_id, session_id: SINGLE.session_id, cancel: true }
    expect(send.mock.calls).toEqual([[cancel], [cancel]])
  })

  it('keeps a failed grace submission visible without repeatedly resending until manual retry', () => {
    vi.useFakeTimers()
    send.mockReturnValueOnce(false)
    show(SINGLE)
    answerWithFreeText('Retry this deliberately')
    act(() => { vi.advanceTimersByTime(ASK_GRACE_MS) })
    expect.soft(screen.queryByRole('alert')).toHaveTextContent('Failed to send your answers — connection dropped. Reconnect and try again.')
    act(() => { vi.advanceTimersByTime(ASK_GRACE_MS) })
    expect(send.mock.calls).toEqual([[answerFrame(SINGLE, 'Retry this deliberately')]])
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answerFrame(SINGLE, 'Retry this deliberately')], [answerFrame(SINGLE, 'Retry this deliberately')]])
  })

  it('announces no connection inline and can answer after connection recovery without remounting', () => {
    useConnectionStore.setState({ connection: null, isConnected: false })
    show(SINGLE)
    answerWithFreeText('Retained offline')
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect.soft(screen.queryByRole('alert')).toHaveTextContent('Cannot send your answers — not connected. Reconnect and try again.')
    useConnectionStore.setState({ connection: socket, isConnected: true })
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answerFrame(SINGLE, 'Retained offline')]])
  })
})

describe('F3 pending-card identity', () => {
  it('starts a promoted card at its first question with no inherited selections or free text', () => {
    show()
    fireEvent.click(screen.getByRole('button', { name: 'Sales' }))
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    answerWithFreeText('Draft for A, never for B')
    expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('2 / 2 answered')
    // Reconnect can replace pending A with B directly, with repeated headers.
    replace({ ...CARD, card_id: 'pending-b' })
    expect.soft(screen.queryByText('Which area?')).toBeVisible()
    expect.soft(screen.getByTestId('ask-user-progress')).toHaveTextContent('0 / 2 answered')
    expect.soft(screen.getByTestId('ask-user-free-text')).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
    fireEvent.click(screen.getByTestId('ask-user-tab-1'))
    expect(screen.getByTestId('ask-user-free-text')).toHaveValue('')
    expect(send.mock.calls).toEqual([])
  })

  it('gives promoted B its own submission latch even when A had submitted', () => {
    show(SINGLE)
    answerWithFreeText('Answer A')
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    const promoted = { ...SINGLE, card_id: 'pending-b' }
    replace(promoted)
    answerWithFreeText('Answer B')
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answerFrame(SINGLE, 'Answer A')], [answerFrame(promoted, 'Answer B')]])
  })

  it('includes session identity when another session has the same card id', () => {
    show(SINGLE)
    answerWithFreeText('Only for session A')
    replace({ ...SINGLE, session_id: 'other-session' })
    expect(screen.getByTestId('ask-user-free-text')).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
  })

  it('preserves draft and duplicate-submit protection across a refresh of the same card', () => {
    show(SINGLE)
    answerWithFreeText('Same-card draft')
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    replace({ ...SINGLE, questions: [...SINGLE.questions] })
    expect(screen.getByTestId('ask-user-free-text')).toHaveValue('Same-card draft')
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answerFrame(SINGLE, 'Same-card draft')]])
  })

  it('starts a fresh grace clock for an auto-resolved promoted card', () => {
    vi.useFakeTimers()
    const resolved: AskCard = { ...SINGLE, auto_resolved: ['Scope'],
      questions: [{ ...SINGLE.questions[0], recommended: 'Sales', default_safe: true }] }
    show(resolved)
    act(() => { vi.advanceTimersByTime(ASK_GRACE_MS - 5000) })
    const promoted = { ...resolved, card_id: 'pending-b' }
    replace(promoted)
    act(() => { vi.advanceTimersByTime(5000) })
    expect.soft(send.mock.calls).toEqual([])
    act(() => { vi.advanceTimersByTime(ASK_GRACE_MS - 5000) })
    expect(send.mock.calls).toEqual([[{
      type: 'ask_user_answer', card_id: promoted.card_id, session_id: promoted.session_id,
      answers: [{ header: 'Scope', selected: ['Sales'], auto_default: true }],
    }]])
  })
})
