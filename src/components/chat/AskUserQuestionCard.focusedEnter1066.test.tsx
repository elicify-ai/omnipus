// #1066 Required behavior 2/4/5 and keyboard acceptance; CHECK2 Scope A F1.
// Enter confirms the focused, still-unanswered final option. Its answer is
// explicit, never the recommendation or an auto_default answer. Real card,
// catalogued controls, answer builder and stores; only socket.send is doubled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AskUserQuestionCard as AskCard, AskUserAnswerFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { AskUserQuestionCard } from './AskUserQuestionCard'

const CARD: AskCard = {
  card_id: 'focused-enter-card', session_id: 'focused-enter-omnipus-main', agent_id: 'omnipus',
  status: 'pending', created_at: '2026-10-14T00:00:00Z',
  questions: [
    { header: 'Area', question: 'Which team needs help?', multi_select: true,
      options: [{ label: 'Sales' }, { label: 'Support' }] },
    { header: 'Apply', question: 'How should we continue?',
      options: [{ label: 'Go' }, { label: 'Change' }, { label: 'Cancel plan' }], recommended: 'Go' },
  ],
}
let socket: WsConnection
let send = vi.fn<WsConnection['send']>()
beforeEach(() => {
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(send)
  useConnectionStore.setState({ connection: socket, isConnected: true, connectionError: null })
})
afterEach(() => {
  socket.disconnect()
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
})

describe('#1066 direct Enter on an unanswered final option', () => {
  it.each([
    { label: 'Go', arrows: '' },
    { label: 'Change', arrows: '{ArrowDown}' },
    { label: 'Cancel plan', arrows: '{ArrowDown}{ArrowDown}' },
  ] as const)('submits the focused "$label" exactly once, never a default answer', async ({ label, arrows }) => {
    const user = userEvent.setup()
    render(<AskUserQuestionCard card={CARD} />)
    const sales = screen.getByRole('button', { name: /^Sales$/i })
    sales.focus()
    await user.keyboard(' ')
    expect(sales).toHaveAttribute('aria-pressed', 'true')
    const next = screen.getByRole('button', { name: /^(next|confirm|continue)(?: question| selection| answer)?$/i })
    next.focus()
    await user.keyboard('{Enter}')
    expect(screen.getByText('How should we continue?')).toBeVisible()
    expect(send.mock.calls).toEqual([])

    // Unlike the older journey, do NOT select the final answer with Space
    // before confirming it. Recommendation and every option remain untouched.
    const go = screen.getByRole('button', { name: /^go(?:\s*recommended)?$/i })
    const change = screen.getByRole('button', { name: /^Change$/i })
    const cancel = screen.getByRole('button', { name: /^Cancel plan$/i })
    for (const option of [go, change, cancel]) expect(option).toHaveAttribute('aria-pressed', 'false')
    go.focus()
    if (arrows) await user.keyboard(arrows)
    expect({ Go: go, Change: change, 'Cancel plan': cancel }[label]).toHaveFocus()
    for (const option of [go, change, cancel]) expect(option).toHaveAttribute('aria-pressed', 'false')
    expect(send.mock.calls).toEqual([])

    await user.keyboard('{Enter}')
    const expected: AskUserAnswerFrame = {
      type: 'ask_user_answer', card_id: CARD.card_id, session_id: CARD.session_id,
      answers: [{ header: 'Area', selected: ['Sales'] }, { header: 'Apply', selected: [label] }],
    }
    // Exact frame and call list reject recommendation substitution, extra
    // auto_default/cancel flags, missing first answers and duplicate sends.
    expect(send.mock.calls).toEqual([[expected]])
  })
})
