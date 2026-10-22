// Docs-verifier N1 / guide: unknown until a recorded total is known.
// Run this file alone: no static app/harness import, store reset or selection
// can warm the foreground before the real constructor's first counter render.
import { expect, it } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'

it('N1: genuine cold foreground first paint is Usage unavailable; only recorded authority, including zero, makes it numeric', async () => {
  localStorage.clear()
  sessionStorage.clear()
  // Fresh fork + first actual app import. Test setup imports no app stores.
  // Neither getInitialState nor getState changes or synchronizes this store.
  const { useChatStore } = await import('@/store/chat/store')
  const initial = useChatStore.getInitialState()
  const { TokenCounter } = await import('@/components/chat/composer/TokenCounter')
  expect(useChatStore.getState(), 'cold instrument: no app action/sync replaced constructor state before render').toBe(initial)
  expect(useChatStore.getState().sessionsById, 'no recorded per-session evidence has been supplied').toStrictEqual({})

  render(<TokenCounter />)
  const counter = screen.getByTestId('session-token-counter')
  const value = screen.getByTestId('session-token-value')
  expect(useChatStore.getState(), 'counter subscription/render itself must not hide the constructor state by synchronizing it').toBe(initial)
  console.info('N1 first-paint evidence', JSON.stringify({ sameConstructorState: useChatStore.getState() === initial, sessionIds: Object.keys(initial.sessionsById), foregroundTokenType: typeof initial.sessionTokens, foregroundTokens: initial.sessionTokens, text: value.textContent, accessibleName: counter.getAttribute('aria-label') }))
  // These still FAIL the case. Collecting lets genuine recorded-zero and
  // positive controls execute after the actual first-paint observations.
  expect.soft(value, 'guide/N1: unseeded first render is explicitly unavailable').toHaveTextContent(/^Usage unavailable$/)
  expect.soft(value, 'constructor0 is NOT evidence of recorded zero').not.toHaveTextContent(/^0 tokens$/)
  expect.soft(counter).toHaveAccessibleName(/usage unavailable/i)

  // Only now allow real foreground selection/sync. No direct counter write,
  // mocked selector, manually manufactured SessionStats or state reset.
  const { useSessionStore } = await import('@/store/session')
  const { SessionStateFrame } = await import('@/lib/api/generated/ws-schemas')
  const sid = 'N1-recorded-counter-session'
  await act(async () => useSessionStore.getState().setActiveSession(sid, 'mia'))
  expect(value, 'per-session unknown remains unavailable before recorded authority').toHaveTextContent(/^Usage unavailable$/)

  const zero: Extract<ServerFrame, { type: 'session_state' }> = {
    type: 'session_state', session_id: sid, user_id: 'N1-counter-reader', pending_approvals: [],
    emitted_at: '2026-10-22T00:00:00Z', history_state: { status: 'available' }, session_tokens_total: 0,
  }
  expect(SessionStateFrame.parse(zero).session_tokens_total, 'instrument: this is an actual declared recorded0 observation').toBe(0)
  await act(async () => useChatStore.getState().handleFrame(zero))
  expect(counter).toHaveAttribute('aria-label', '0 tokens used')
  expect(value).toHaveTextContent(/^0 tokens$/)
  console.info('N1 recorded-zero control', JSON.stringify({ total: useChatStore.getState().sessionTokens, text: value.textContent, accessibleName: counter.getAttribute('aria-label') }))

  const positive: typeof zero = { ...zero, session_tokens_total: 46323 }
  expect(SessionStateFrame.parse(positive).session_tokens_total).toBe(46323)
  await act(async () => useChatStore.getState().handleFrame(positive))
  expect(counter, 'positive recorded control rules out a hardcoded numeric0 branch').toHaveAttribute('aria-label', '46323 tokens used')
  expect(useChatStore.getState().sessionsById[sid].sessionTokens).toBe(46323)
  console.info('N1 recorded-positive control', JSON.stringify({ total: useChatStore.getState().sessionTokens, accessibleName: counter.getAttribute('aria-label') }))
})
