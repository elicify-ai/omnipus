// WU-10 fruit #1066: Required behavior 1–5 and acceptance criteria.
// Also spec FR-109 / BDD-12.14 keyboard-first journeys. The real card,
// catalogued controls and answer store run; only socket.send is doubled.
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AskUserQuestionCard as AskCard, AskUserAnswerFrame } from '@/lib/api/generated/asyncapi-types'
import { AskUserQuestionCard } from './AskUserQuestionCard'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'

const MULTI: AskCard = {
  card_id: 'keyboard-card', session_id: 'omnipus-question-main', agent_id: 'omnipus',
  status: 'pending', created_at: '2026-10-13T00:00:00Z',
  questions: [
    { header: 'Coverage', question: 'Which areas should the agent cover?', multi_select: true,
      options: [{ label: 'Sales' }, { label: 'Support' }, { label: 'Research' }] },
    { header: 'Apply', question: 'How should we continue?', options: [{ label: 'Go' }, { label: 'Change' }, { label: 'Cancel plan' }], recommended: 'Go' },
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
function next() { return screen.getByRole('button', { name: /^(next|confirm|continue)(?: question| selection| answer)?$/i }) }
function finalSubmit() { return screen.getByRole('button', { name: /^(answer|submit|confirm)(?: answers| all| card)?$/i }) }
// Synchronous browser-key activation adapter for the fake-clock predicate.
// jsdom does not perform a key's native click default; send that detail=0
// click only when neither key event was prevented. Full keyboard journeys
// above/below still use the real user-event driver and real catalogued controls.
function activateKey(control: HTMLElement, key: ' ' | 'Enter') {
  control.focus()
  const code = key === ' ' ? 'Space' : 'Enter'
  const down = fireEvent.keyDown(control, { key, code })
  const up = fireEvent.keyUp(control, { key, code })
  if (down && up) fireEvent.click(control, { detail: 0 })
}
function expectCompleteAnswer() {
  const expected: AskUserAnswerFrame = {
    type: 'ask_user_answer', card_id: MULTI.card_id, session_id: MULTI.session_id,
    answers: [{ header: 'Coverage', selected: ['Sales', 'Support'] }, { header: 'Apply', selected: ['Go'] }],
  }
  expect(send.mock.calls).toEqual([[expected]])
}

describe('#1066 keyboard-first per-question confirmation', () => {
  it('#1066 AC2 shows a disabled confirmation until the current question has an answer', () => {
    render(<AskUserQuestionCard card={MULTI} />)
    expect(next()).toBeVisible()
    expect(next()).toBeDisabled()
    expect(send.mock.calls).toEqual([])
  })
  it('#1066 AC3 confirms multiple choices to the next question by pointer, not by clicking its tab', async () => {
    const user = userEvent.setup()
    render(<AskUserQuestionCard card={MULTI} />)
    await user.click(screen.getByRole('button', { name: /^sales$/i }))
    await user.click(screen.getByRole('button', { name: /^support$/i }))
    expect(screen.getByText('Which areas should the agent cover?')).toBeVisible()
    expect(next()).toBeEnabled()
    await user.click(next())
    expect(screen.getByText('How should we continue?')).toBeVisible()
    expect(screen.queryByText('Which areas should the agent cover?')).not.toBeInTheDocument()
    expect(send.mock.calls).toEqual([])
  })
  it('#1066 AC4 ArrowDown moves option focus without silently selecting or submitting', async () => {
    render(<AskUserQuestionCard card={MULTI} />)
    const sales = screen.getByRole('button', { name: /^sales$/i })
    const support = screen.getByRole('button', { name: /^support$/i })
    sales.focus()
    await userEvent.setup().keyboard('{ArrowDown}')
    expect(support).toHaveFocus()
    expect(sales).toHaveAttribute('aria-pressed', 'false')
    expect(support).toHaveAttribute('aria-pressed', 'false')
    expect(send.mock.calls).toEqual([])
  })
  it('#1066 AC1/4 answers a mixed card end to end with Space, arrows and Enter only', async () => {
    const user = userEvent.setup()
    render(<AskUserQuestionCard card={MULTI} />)
    screen.getByRole('button', { name: /^sales$/i }).focus()
    await user.keyboard(' {ArrowDown} ')
    expect(screen.getByRole('button', { name: /^sales$/i })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /^support$/i })).toHaveAttribute('aria-pressed', 'true')
    next().focus()
    await user.keyboard('{Enter}')
    expect(screen.getByText('How should we continue?')).toBeVisible()
    screen.getByRole('button', { name: /^go(?:\s*recommended)?$/i }).focus()
    await user.keyboard(' ')
    finalSubmit().focus()
    await user.keyboard('{Enter}')
    expectCompleteAnswer()
  })
  it('#1066 AC5 pointer confirmation preserves every selected value in the final submit', async () => {
    const user = userEvent.setup()
    render(<AskUserQuestionCard card={MULTI} />)
    await user.click(screen.getByRole('button', { name: /^sales$/i }))
    await user.click(screen.getByRole('button', { name: /^support$/i }))
    await user.click(next())
    await user.click(screen.getByRole('button', { name: /^go(?:\s*recommended)?$/i }))
    await user.click(finalSubmit())
    expectCompleteAnswer()
  })
  // #1066 Required behavior 4 / CHECK F1: selection is NOT confirmation.
  // Drain scheduled callbacks before confirming so a delayed auto-advance dies.
  // This is the calibrated C03-v2 predicate, not an elapsed-time assertion.
  it.each(['pointer', 'keyboard'] as const)(
    '#1066 Required behavior 4 single-choice %s selection stays put until explicit confirmation', (input) => {
      vi.useFakeTimers()
      try {
        const single: AskCard = { ...MULTI, questions: [{ ...MULTI.questions[0], multi_select: false }, MULTI.questions[1]] }
        render(<AskUserQuestionCard card={single} />)
        expect(next()).toBeVisible()
        const sales = screen.getByRole('button', { name: /^sales$/i })
        if (input === 'pointer') fireEvent.click(sales)
        else activateKey(sales, ' ')
        expect(sales).toHaveAttribute('aria-pressed', 'true')
        act(() => { vi.runOnlyPendingTimers() })
        expect(screen.getByText('Which areas should the agent cover?')).toBeVisible()
        expect(screen.queryByText('How should we continue?')).not.toBeInTheDocument()
        expect(next()).toBeEnabled()
        expect(send.mock.calls).toEqual([])
        if (input === 'pointer') fireEvent.click(next())
        else activateKey(next(), 'Enter')
        expect(screen.getByText('How should we continue?')).toBeVisible()
        expect(send.mock.calls).toEqual([])
      } finally {
        vi.useRealTimers()
      }
    },
  )
  it('#1066 negative case deselecting the last multi choice disables confirm and sends nothing', async () => {
    const user = userEvent.setup()
    render(<AskUserQuestionCard card={MULTI} />)
    const sales = screen.getByRole('button', { name: /^sales$/i })
    await user.click(sales)
    expect(next()).toBeEnabled()
    await user.click(sales)
    expect(next()).toBeDisabled()
    expect(screen.getByText('Which areas should the agent cover?')).toBeVisible()
    expect(send.mock.calls).toEqual([])
  })
  it('FR-109 Go is never preselected when the final question opens', async () => {
    render(<AskUserQuestionCard card={MULTI} />)
    await userEvent.setup().click(screen.getByRole('button', { name: /2 apply/i }))
    expect(screen.getByRole('button', { name: /^go(?:\s*recommended)?$/i })).toHaveAttribute('aria-pressed', 'false')
    expect(finalSubmit()).toBeDisabled()
    expect(send.mock.calls).toEqual([])
  })
})
