// R3-F2: a store subscriber that throws on the snapshot commit AND again on
// the restoration must not strand the restored reader in a "loaded" state:
// the visible "Could not load messages." + Retry is still published.
// Real app/parser/frame owner/merger; only the fault subscriber is injected.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, screen, within } from '@testing-library/react'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import { useChatStore } from '@/store/chat'
import { MAIN_ID } from '../../../tests/l1-room-ui/harness'
import { entry, page, pos, replay, snapshot, STAMP } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, stopHistory } from '../../../tests/chat-history-paging/application'

// Fault injection for the case the store never assigns: the restoration's own
// row computation throws BEFORE Zustand publishes anything. Armed only by the
// test below; every other call is the real implementation.
const restoreFault = vi.hoisted(() => ({ armed: false }))
vi.mock('@/store/chat/history-state', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat/history-state')>()
  return { ...actual, historyRowsPatch: (...args: Parameters<typeof actual.historyRowsPatch>) => {
    if (restoreFault.armed) { restoreFault.armed = false; throw new TypeError('restoration computed nothing') }
    return actual.historyRowsPatch(...args)
  } }
})

let app: Awaited<ReturnType<typeof openHistory>> | undefined
let unsubscribe: (() => void) | undefined
afterEach(async () => {
  restoreFault.armed = false
  unsubscribe?.(); unsubscribe = undefined
  await stopHistory(app); app = undefined
})

function frames(history: SessionHistoryResponse): ServerFrame[] {
  return [
    { ...snapshot(history), seq: 200 },
    { type: 'session_state', session_id: MAIN_ID, user_id: 'repeat-fault-reader', history_state: { status: 'available' }, pending_approvals: [], emitted_at: STAMP },
    ...history.entries.map(source => replay(source, MAIN_ID)),
    { type: 'done', session_id: MAIN_ID },
    { type: 'catch_up_complete', session_id: MAIN_ID, boot_id: 'history-boot', seq: 200, mode: 'snapshot' },
  ]
}

describe('snapshot rejection under a repeatedly throwing subscriber', () => {
  it('restores the prior reader AND publishes the failed load when restoration throws too', async () => {
    app = await openHistory(page(Array.from({ length: 40 }, (_, index) => entry(501 + index)), { sessionId: MAIN_ID }))
    const prior = useChatStore.getState().history.readerPositions.slice()
    expect(prior, 'positive control: a valid prior reader committed').toStrictEqual(Array.from({ length: 40 }, (_, index) => pos(501 + index)))
    const replacement = pos(901)
    let commits = 0
    let restorations = 0
    const commitFault = new TypeError('commit fault')
    const restorationFault = new TypeError('restoration fault')
    // Throw on the replacement commit, then again on the very next message-map
    // replacement (the restoration of the prior reader).
    unsubscribe = useChatStore.subscribe((state, previous) => {
      if (state.messagesById === previous.messagesById) return
      if (commits === 0 && state.sessionsById[MAIN_ID]?.history.readerPositions[0] === replacement) { commits++; throw commitFault }
      if (commits === 1 && restorations === 0) { restorations++; throw restorationFault }
    })
    try {
      act(() => frames(page([entry(901, { content: 'REPLACEMENT_BODY' })], { sessionId: MAIN_ID })).forEach(frame => useChatStore.getState().handleFrame(frame)))
    } catch (error) {
      // Only this exact injected commit fault may propagate. A different
      // programming error after restoration must fail, even if state is valid.
      if (error !== commitFault) throw error
      expect(error).toBeInstanceOf(TypeError)
      expect(error).toBe(commitFault)
    }
    unsubscribe(); unsubscribe = undefined
    expect(commits, 'positive fault instrument: the replacement commit reached the subscriber').toBe(1)
    expect(restorations, 'positive fault instrument: the restoration reached the subscriber too').toBe(1)
    const history = useChatStore.getState().history
    expect(history.readerPositions, 'the prior reader is restored').toStrictEqual(prior)
    expect(history.historyLoaded, 'a failed replacement cannot leave the history marked loaded').toBe(false)
    expect(history.requests.latest.status).toBe('error')
    const region = screen.getByRole('region', { name: 'Conversation history' })
    const alert = await within(region).findByRole('alert')
    expect(alert, 'the visible rejection and its Retry are published').toHaveTextContent('Could not load messages.')
    expect(within(alert).getByRole('button', { name: /^Retry$/u })).toBeTruthy()
    expect(within(region).queryByText('REPLACEMENT_BODY', { exact: true }), 'the rejected body is not left on screen').toBeNull()
  })

  it('publishes the failed load when the restoration throws BEFORE anything is stored', async () => {
    app = await openHistory(page(Array.from({ length: 40 }, (_, index) => entry(501 + index)), { sessionId: MAIN_ID }))
    const replacement = pos(901)
    let commits = 0
    // Fail the commit through a subscriber, and arm a fault that makes the
    // restoration throw while computing its patch, before any assignment.
    unsubscribe = useChatStore.subscribe((state, previous) => {
      if (state.messagesById === previous.messagesById) return
      if (commits === 0 && state.sessionsById[MAIN_ID]?.history.readerPositions[0] === replacement) { commits++; restoreFault.armed = true; throw new TypeError('commit fault') }
    })
    try {
      act(() => frames(page([entry(901, { content: 'REPLACEMENT_BODY' })], { sessionId: MAIN_ID })).forEach(frame => useChatStore.getState().handleFrame(frame)))
    } catch { /* the original commit fault may propagate; the state below is the oracle */ }
    unsubscribe(); unsubscribe = undefined
    expect(commits, 'positive fault instrument: the replacement commit reached the subscriber').toBe(1)
    expect(restoreFault.armed, 'positive fault instrument: the restoration actually hit the injected fault').toBe(false)
    const history = useChatStore.getState().history
    expect(history.historyLoaded, 'an unpublished restoration still cannot leave the history marked loaded').toBe(false)
    expect(history.requests.latest.status).toBe('error')
    const region = screen.getByRole('region', { name: 'Conversation history' })
    const alert = await within(region).findByRole('alert')
    expect(alert, 'the visible rejection and its Retry are published').toHaveTextContent('Could not load messages.')
  })
})
