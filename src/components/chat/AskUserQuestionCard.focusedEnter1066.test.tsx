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

// Founder decision 2026-10-14 (team-lead relay), alongside #1066:
// once answered, Enter confirms the EARLIER answer even if focus moved to a
// different option. A click changes the answer; focus movement alone does not.
// New helper is used only by these added cases; the original RED matrix above
// and all its expectations remain unchanged.
async function openAnsweredFinalQuestion(card: AskCard = CARD) {
  const user = userEvent.setup()
  render(<AskUserQuestionCard card={card} />)
  await user.click(screen.getByRole('button', { name: /^Sales$/i }))
  await user.click(screen.getByRole('button', { name: /^(next|confirm|continue)(?: question| selection| answer)?$/i }))
  expect(screen.getByText('How should we continue?')).toBeVisible()
  expect(send.mock.calls).toEqual([])
  const options = {
    Go: screen.getByRole('button', { name: /^go(?:\s*recommended)?$/i }),
    Change: screen.getByRole('button', { name: /^Change$/i }),
    'Cancel plan': screen.getByRole('button', { name: /^Cancel plan$/i }),
  }
  for (const option of Object.values(options)) expect(option).toHaveAttribute('aria-pressed', 'false')
  return { user, options }
}
function expectExplicitFinalAnswer(selected: string[]) {
  const expected: AskUserAnswerFrame = {
    type: 'ask_user_answer', card_id: CARD.card_id, session_id: CARD.session_id,
    answers: [{ header: 'Area', selected: ['Sales'] }, { header: 'Apply', selected }],
  }
  expect(send.mock.calls).toEqual([[expected]])
}

describe('#1066 already-answered Enter — founder decision 2026-10-14 (team-lead relay)', () => {
  it.each([
    { earlier: 'Go', focused: 'Change', arrows: '{ArrowDown}' },
    { earlier: 'Change', focused: 'Go', arrows: '{ArrowUp}' },
    { earlier: 'Cancel plan', focused: 'Change', arrows: '{ArrowUp}' },
  ] as const)('K-Answered retains earlier "$earlier" when Enter confirms a different focused "$focused"', async ({ earlier, focused, arrows }) => {
    const { user, options } = await openAnsweredFinalQuestion()
    await user.click(options[earlier])
    expect(options[earlier]).toHaveAttribute('aria-pressed', 'true')
    await user.keyboard(arrows)
    expect(options[focused]).toHaveFocus()
    expect(options[focused]).toHaveAttribute('aria-pressed', 'false')
    expect(options[earlier]).toHaveAttribute('aria-pressed', 'true')
    expect(send.mock.calls).toEqual([])
    await user.keyboard('{Enter}')
    expectExplicitFinalAnswer([earlier])
  })
  it('K-Answered multi-choice Enter retains the earlier set, not the other focused option', async () => {
    const card: AskCard = { ...CARD, questions: [CARD.questions[0], { ...CARD.questions[1], multi_select: true }] }
    const { user, options } = await openAnsweredFinalQuestion(card)
    await user.click(options.Go)
    await user.click(options['Cancel plan'])
    expect(options.Go).toHaveAttribute('aria-pressed', 'true')
    expect(options['Cancel plan']).toHaveAttribute('aria-pressed', 'true')
    await user.keyboard('{ArrowUp}')
    expect(options.Change).toHaveFocus()
    expect(options.Change).toHaveAttribute('aria-pressed', 'false')
    expect(send.mock.calls).toEqual([])
    await user.keyboard('{Enter}')
    expectExplicitFinalAnswer(['Go', 'Cancel plan'])
  })
  it('K-Click-change clicking Change replaces an earlier Go before Enter confirms it', async () => {
    const { user, options } = await openAnsweredFinalQuestion()
    await user.click(options.Go)
    expect(options.Go).toHaveAttribute('aria-pressed', 'true')
    await user.click(options.Change)
    expect(options.Go).toHaveAttribute('aria-pressed', 'false')
    expect(options.Change).toHaveAttribute('aria-pressed', 'true')
    expect(options.Change).toHaveFocus()
    expect(send.mock.calls).toEqual([])
    await user.keyboard('{Enter}')
    expectExplicitFinalAnswer(['Change'])
  })
})
