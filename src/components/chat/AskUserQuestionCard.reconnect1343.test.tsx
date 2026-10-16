// FCR F1 (#1343): a locally sent action whose rejection was lost is retried
// only after reconnect reconciliation confirms the exact same pending card.
// Real parser/reducer/card/stores; only the socket-send boundary is doubled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { AskUserAnswerFrame, AskUserQuestionCard as AskCard, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { parseFrameSafe, WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { ASK_GRACE_MS, AskUserQuestionThreadTail } from './AskUserQuestionCard'

const CARD: AskCard = {
  card_id: 'lost-rejection-card', session_id: 'lost-rejection-session', agent_id: 'omnipus',
  status: 'pending', created_at: '2026-10-17T00:00:00Z',
  questions: [
    { header: 'Scope', question: 'Which area?', options: [{ label: 'Sales' }, { label: 'Support' }] },
    { header: 'Apply', question: 'How should we continue?', options: [{ label: 'Draft' }, { label: 'Send' }] },
  ],
}
const TEXT = 'Preserve my unsent decision'
const RETRY_NOTICE = 'This question card is still waiting after reconnecting. Review your answers, then choose Answer or Cancel to retry.'
let sockets: WsConnection[]
let send: ReturnType<typeof vi.fn<WsConnection['send']>>

function makeSocket() {
  const socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  vi.spyOn(socket, 'send').mockImplementation(send)
  sockets.push(socket)
  return socket
}
beforeEach(() => {
  vi.useFakeTimers()
  sockets = []
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useConnectionStore.setState({ connection: makeSocket(), isConnected: true })
})
afterEach(() => {
  cleanup()
  for (const socket of sockets) socket.disconnect()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.restoreAllMocks()
})
function receive(frame: ServerFrame) {
  const parsed = parseFrameSafe(JSON.stringify(frame))
  expect(parsed).toEqual(frame)
  act(() => { useChatStore.getState().handleFrame(parsed as ServerFrame) })
}
function show() {
  useSessionStore.setState({ activeSessionId: CARD.session_id })
  receive({ type: 'ask_user_question', card: CARD })
  return render(<AskUserQuestionThreadTail />)
}
function snapshot(cards: AskCard[] = [CARD]) {
  receive({ type: 'session_state', user_id: 'test-owner', session_id: CARD.session_id,
    pending_approvals: [], pending_asks: cards, emitted_at: '2026-10-17T00:01:00Z' })
}
function disconnect() {
  // The production WsLifecycle calls these same actions on socket close.
  act(() => {
    useConnectionStore.getState().recordDisconnect(null)
    useChatStore.getState().clearStreamingState()
  })
}
function reconnect() {
  act(() => { useConnectionStore.getState().setConnected(true) })
}
function fillDraft() {
  fireEvent.click(screen.getByRole('button', { name: 'Sales' }))
  fireEvent.click(screen.getByRole('button', { name: 'Next' }))
  fireEvent.change(screen.getByRole('textbox'), { target: { value: TEXT } })
}
function answer(card: AskCard = CARD): AskUserAnswerFrame {
  return { type: 'ask_user_answer', card_id: card.card_id, session_id: card.session_id,
    answers: [{ header: 'Scope', selected: ['Sales'] }, { header: 'Apply', free_text: TEXT }] }
}
function expectLatched() {
  expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  expect(screen.getByRole('textbox')).toBeDisabled()
  expect(screen.getByTestId('ask-user-tab-0')).toBeDisabled()
}

describe('FCR F1 lost card rejection across reconnect', () => {
  it.each(['Answer', 'Cancel'] as const)('recovers %s only after the same pending-card snapshot, retaining the live draft for deliberate retry', (action) => {
    show()
    fillDraft()
    const originalInput = screen.getByRole('textbox')
    fireEvent.click(screen.getByRole('button', { name: action }))
    const frame: AskUserAnswerFrame = action === 'Answer' ? answer() : {
      type: 'ask_user_answer', card_id: CARD.card_id, session_id: CARD.session_id, cancel: true,
    }
    expect(send.mock.calls).toEqual([[frame]])
    expectLatched()
    // The server save fails, but its rejection never reaches this tab.
    disconnect()
    expectLatched()
    reconnect()
    expectLatched() // Connectivity alone does not establish whether A was consumed.
    receive({ type: 'ask_user_question', card: CARD })
    expectLatched() // An ordinary update is not the reconnect snapshot.
    snapshot()
    expect.soft(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    expect.soft(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
    expect.soft(screen.getByRole('textbox')).toBeEnabled()
    expect.soft(screen.getByTestId('ask-user-tab-0')).toBeEnabled()
    expect.soft(screen.queryByRole('alert')).toHaveTextContent(RETRY_NOTICE)
    expect(screen.getByRole('textbox')).toBe(originalInput) // No draft-losing remount.
    expect(originalInput).toHaveValue(TEXT)
    fireEvent.click(screen.getByTestId('ask-user-tab-0'))
    expect(screen.getByRole('button', { name: 'Sales' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByTestId('ask-user-tab-1'))
    expect(screen.getByRole('textbox')).toHaveValue(TEXT)
    act(() => { vi.advanceTimersByTime(ASK_GRACE_MS * 2) })
    expect(send.mock.calls).toEqual([[frame]]) // No hidden grace retry.
    fireEvent.click(screen.getByRole('button', { name: action }))
    expect(send.mock.calls).toEqual([[frame], [frame]])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    snapshot()
    expectLatched() // This new action was not interrupted by the old disconnect.
    expect(send.mock.calls).toEqual([[frame], [frame]])
  })

  it('keeps an uninterrupted action latched across an ordinary same-card snapshot', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    snapshot()
    expectLatched()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox')).toHaveValue(TEXT)
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it('does not accept a pending snapshot until connected and a fresh reconciliation arrives', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    disconnect()
    snapshot()
    expectLatched()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    reconnect()
    expectLatched()
    snapshot()
    expect(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    expect(screen.getByRole('textbox')).toHaveValue(TEXT)
    expect(screen.getByRole('alert')).toHaveTextContent(RETRY_NOTICE)
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it('tracks replacement of the connection object while waiting for the same-card snapshot', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    act(() => { useConnectionStore.getState().setConnection(makeSocket()) })
    expectLatched()
    snapshot()
    expect(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    expect(screen.getByRole('textbox')).toHaveValue(TEXT)
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answer()], [answer()]])
  })

  it('does not let an earlier disconnect release a new action after a delivered rejection', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    disconnect()
    reconnect()
    receive({ type: 'error', session_id: CARD.session_id, ask_user_card_id: CARD.card_id,
      message: 'Could not save your answer. Try again.' })
    expect(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    snapshot()
    expectLatched()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(send.mock.calls).toEqual([[answer()], [answer()]])
  })
})

describe('reconnect recovery does not replay a different or consumed card', () => {
  it('gives a promoted card fresh state without carrying over the interrupted action', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    disconnect()
    reconnect()
    const next: AskCard = { ...CARD, card_id: 'promoted-after-disconnect' }
    snapshot([next])
    expect(screen.getByText('Which area?')).toBeVisible()
    expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('0 / 2 answered')
    expect(screen.getByRole('textbox')).toHaveValue('')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    snapshot([next])
    expectLatched()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(send.mock.calls).toEqual([[answer()], [answer(next)]])
  })

  it('does not confirm a displayed card from a snapshot naming the same card id in another session', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    disconnect()
    reconnect()
    snapshot([{ ...CARD, session_id: 'other-session' }])
    expectLatched()
    expect(screen.getByRole('textbox')).toHaveValue(TEXT)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it.each(['answered', 'cancelled'] as const)('never reopens a %s card when an old snapshot still lists it as pending', (status) => {
    const { container } = show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    disconnect()
    reconnect()
    const terminal: AskCard = { ...CARD, status }
    receive({ type: 'ask_user_question', card: terminal })
    snapshot()
    expect(container).toBeEmptyDOMElement()
    expect(useChatStore.getState().sessionsById[CARD.session_id].pendingAsk).toEqual(terminal)
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it('does not retry a consumed card absent from the reconnect snapshot', () => {
    const { container } = show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    disconnect()
    reconnect()
    snapshot([])
    expect(container).toBeEmptyDOMElement()
    expect(useChatStore.getState().sessionsById[CARD.session_id].pendingAsk).toBeNull()
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it('retains an ordinary draft without a retry error when no action was outstanding at disconnect', () => {
    show()
    fillDraft()
    const originalInput = screen.getByRole('textbox')
    disconnect()
    reconnect()
    snapshot()
    expect(screen.getByRole('textbox')).toBe(originalInput)
    expect(originalInput).toHaveValue(TEXT)
    expect(originalInput).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(send.mock.calls).toEqual([])
  })
})
