// Decision A / SFH F2 (#1343): generated card-scoped errors are operational
// notifications, never turn endings or permission to replay a consumed card.
// Real parser, frame reducer, stores and mounted card; only socket.send is doubled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { AskUserAnswerFrame, AskUserQuestionCard as AskCard, ErrorFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { parseFrameSafe, WsConnection } from '@/lib/ws'
import { useChatStore, getMessages } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { AskUserQuestionThreadTail } from './AskUserQuestionCard'

const CARD: AskCard = {
  card_id: 'save-failed-a', session_id: 'save-retry-session', agent_id: 'omnipus',
  status: 'pending', created_at: '2026-10-16T12:00:00Z',
  questions: [
    { header: 'Scope', question: 'Which area?', options: [{ label: 'Sales' }, { label: 'Support' }] },
    { header: 'Apply', question: 'How should we continue?', options: [{ label: 'Draft' }, { label: 'Send' }] },
  ],
}
const MESSAGE = 'Could not save your answer. Try again.'
const TEXT = 'Keep this exact draft'
let socket: WsConnection
let send: ReturnType<typeof vi.fn<WsConnection['send']>>

beforeEach(() => {
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(send)
  useConnectionStore.setState({ connection: socket, isConnected: true })
})
afterEach(() => {
  cleanup()
  socket.disconnect()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.restoreAllMocks()
})

function bucket(card: AskCard) {
  return { ...emptySessionState(), pendingAsk: card, isStreaming: true,
    activeTurnId: 'still-running-turn', activeTurnAgentId: 'omnipus',
    messagesById: { live: { id: 'live', role: 'assistant' as const, content: 'Still working',
      timestamp: '2026-10-16T12:00:00Z', status: 'streaming' as const, isStreaming: true } },
    messageOrder: ['live'] }
}
function show(card: AskCard = CARD) {
  useSessionStore.setState({ activeSessionId: card.session_id })
  useChatStore.setState({ sessionsById: { [card.session_id]: bucket(card) }, pendingAsk: card,
    isStreaming: true })
  render(<AskUserQuestionThreadTail />)
}
function receive(frame: ServerFrame) {
  const parsed = parseFrameSafe(JSON.stringify(frame))
  expect(parsed).toEqual(frame) // Do not bypass the generated runtime validator.
  act(() => { useChatStore.getState().handleFrame(parsed as ServerFrame) })
}
function reject(card: AskCard = CARD, message = MESSAGE) {
  const frame: ErrorFrame = { type: 'error', session_id: card.session_id,
    ask_user_card_id: card.card_id, message }
  receive(frame)
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
function expectTurnUntouched(card: AskCard = CARD) {
  const current = useChatStore.getState().sessionsById[card.session_id]
  expect(current.pendingAsk).toEqual(card)
  expect(current.isStreaming).toBe(true)
  expect(current.activeTurnId).toBe('still-running-turn')
  expect(current.activeTurnAgentId).toBe('omnipus')
  expect(current.messagesById.live).toEqual({ id: 'live', role: 'assistant', content: 'Still working',
    timestamp: '2026-10-16T12:00:00Z', status: 'streaming', isStreaming: true })
}
function notices(sessionId: string) {
  return getMessages(useChatStore.getState().sessionsById[sessionId])
    .filter((message) => message.refusalNotice).map((message) => message.content)
}

describe('F2 exact-card save-rejection recovery', () => {
  it.each(['Answer', 'Cancel'] as const)('unlocks %s after a failed save, retains every draft and retries the same id', (action) => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: action }))
    expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    reject()
    expect.soft(screen.queryByRole('alert')).toHaveTextContent(MESSAGE)
    expect.soft(screen.getByRole('button', { name: 'Answer' })).toBeEnabled()
    expect.soft(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
    expect.soft(screen.getByRole('textbox')).toHaveValue(TEXT)
    expectTurnUntouched()
    fireEvent.click(screen.getByTestId('ask-user-tab-0'))
    expect(screen.getByRole('button', { name: 'Sales' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByTestId('ask-user-tab-1'))
    fireEvent.click(screen.getByRole('button', { name: action }))
    const expected = action === 'Answer' ? answer() : { type: 'ask_user_answer', card_id: CARD.card_id, session_id: CARD.session_id, cancel: true }
    expect(send.mock.calls).toEqual([[expected], [expected]])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // The server's terminal card, not an error or socket send, closes the UI.
    const terminal: AskCard = { ...CARD, status: action === 'Answer' ? 'answered' : 'cancelled' }
    receive({ type: 'ask_user_question', card: terminal })
    expect(screen.queryByTestId('ask-user-question-card')).not.toBeInTheDocument()
    expect(useChatStore.getState().sessionsById[CARD.session_id].pendingAsk).toEqual(terminal)
  })

  it('does not mistake a generic error with question-card prose for a correlated rejection', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    receive({ type: 'error', session_id: CARD.session_id, message: 'ask_user_answer: failed to save' })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it('uses the normal sanitized display for a matching card error without deriving identity from its prose', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    reject(CARD, 'ask_user_answer: save failed at /private/storage')
    expect.soft(screen.queryByRole('alert')).toHaveTextContent('Something went wrong — please try again.')
    expectTurnUntouched()
  })
})

describe('card-scoped notifications never change another card or end a turn', () => {
  it('keeps a promoted card latched and unchanged when the previous card reports a late error', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    const next: AskCard = { ...CARD, card_id: 'promoted-b' }
    receive({ type: 'ask_user_question', card: next })
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    reject(CARD)
    expect.soft(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect.soft(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
    expect(screen.getByRole('textbox')).toHaveValue(TEXT)
    expectTurnUntouched(next)
    expect(notices(next.session_id)).toEqual([MESSAGE])
    expect(send.mock.calls).toEqual([[answer()], [answer(next)]])
  })

  it('matches session identity as well as card identity, without unlocking the foreground card', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    const other: AskCard = { ...CARD, session_id: 'other-session' }
    act(() => { useChatStore.setState((state) => ({ sessionsById: { ...state.sessionsById, [other.session_id]: bucket(other) } })) })
    reject(other)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
    expectTurnUntouched()
    expectTurnUntouched(other)
    expect(notices(other.session_id)).toEqual([MESSAGE])
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it.each(['answered', 'cancelled'] as const)('never reopens a %s card when an accepted action fails to resume', (status) => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    const terminal: AskCard = { ...CARD, status }
    receive({ type: 'ask_user_question', card: terminal })
    reject(CARD, 'Your answer was accepted, but the paused turn could not resume.')
    expect(screen.queryByTestId('ask-user-question-card')).not.toBeInTheDocument()
    expectTurnUntouched(terminal)
    expect(notices(CARD.session_id)).toEqual(['Your answer was accepted, but the paused turn could not resume.'])
    expect(send.mock.calls).toEqual([[answer()]])
  })

  it('requires fresh decisions when a reconnect snapshot promotes a new card with repeated question headers', () => {
    show()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    const next: AskCard = { ...CARD, card_id: 'snapshot-promoted-b' }
    receive({ type: 'session_state', user_id: 'test-owner', session_id: CARD.session_id,
      emitted_at: '2026-10-16T12:01:00Z', pending_approvals: [], pending_asks: [next],
      active_turn: { turn_id: 'still-running-turn', agent_id: 'omnipus', started_at: '2026-10-16T12:00:00Z' } })
    expect(screen.getByText('Which area?')).toBeVisible()
    expect(screen.getByTestId('ask-user-progress')).toHaveTextContent('0 / 2 answered')
    expect(screen.getByRole('button', { name: 'Sales' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('textbox')).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Answer' })).toBeDisabled()
    fillDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    expect(send.mock.calls).toEqual([[answer()], [answer(next)]])
  })

  it('surfaces an unknown-session card error without creating a session or ending the foreground turn', () => {
    show()
    const unknown = { ...CARD, session_id: 'unknown-session' }
    reject(unknown)
    expectTurnUntouched()
    expect(useChatStore.getState().sessionsById[unknown.session_id]).toBeUndefined()
    expect(useConnectionStore.getState().connectionError).toBe(MESSAGE)
  })

  it('rejects a card error missing session_id at the real wire validator', () => {
    show()
    const malformed: ErrorFrame = { type: 'error', ask_user_card_id: CARD.card_id, message: MESSAGE }
    expect(parseFrameSafe(JSON.stringify(malformed))).toBeNull()
    expectTurnUntouched()
    expect(send.mock.calls).toEqual([])
  })

  it('does not fall back to the current session when a card notification has an empty session_id', () => {
    show()
    reject({ ...CARD, session_id: '' })
    expectTurnUntouched()
    expect(Object.keys(useChatStore.getState().sessionsById)).toEqual([CARD.session_id])
    expect(useConnectionStore.getState().connectionError).toBe(MESSAGE)
  })
})
