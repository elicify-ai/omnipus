// F5 (latest user addition), D16/B55/B56/D-U2: legacy list metadata is NOT
// proof of an authoritative aggregate after its loss. Real list HTTP adapter,
// Search sessions UI, useSelectSession, attach/history owners and TokenCounter.
// Network only; no store/hook/counter/navigation mock or direct counter write.
import { afterEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Session, SessionPage } from '@/lib/api/generated/openapi-types'
import type { SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { SessionPage as SessionPageSchema } from '@/lib/api/generated/schemas'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { MAIN_ID, WS, json } from '../../tests/l1-room-ui/harness'
import { availableDetail, deferred, entry, expectValidPage, page, pos, session, STAMP } from '../../tests/chat-history-paging/fixtures'
import { openHistory, sendFrames, sendSnapshot, stopHistory } from '../../tests/chat-history-paging/application'

const TARGET = 'F5-missing-aggregate-list-session'
const TITLE = 'F5 history without an aggregate baseline'
let app: Awaited<ReturnType<typeof openHistory>> | undefined
let finishDetail: (() => void) | undefined

afterEach(async () => {
  if (finishDetail) await act(async () => finishDetail!())
  finishDetail = undefined
  await stopHistory(app); app = undefined
})

async function settleSocketFrame() {
  // WsConnection dispatches on a browser animation frame. Await the next
  // ordered browser-frame barrier, not a guessed sleep or elapsed-time oracle.
  await act(async () => new Promise<void>(resolve => requestAnimationFrame(() => resolve())))
}

function expectUnavailable(phase: string) {
  expect.soft(screen.getByTestId('session-token-value'), `${phase}: absence of authority is not legacy numeric usage`).toHaveTextContent(/^Usage unavailable$/u)
  expect.soft(screen.getByTestId('session-token-counter'), `${phase}: accessible value is also unknown`).toHaveAttribute('aria-label', 'Session token usage unavailable')
}

describe('F5 real list selection cannot establish lost usage authority', () => {
  it.each([0, 46323])('legacy list total%s stays unknown until an actual recorded state/page supplies authority', async legacy => {
    const user = userEvent.setup()
    const first = page(Array.from({ length: 40 }, (_, index) => entry(501 + index)), { sessionId: MAIN_ID })
    app = await openHistory(first)
    // D16 deliberately has NO snapshot absolute-total field. Supply the
    // initial main's authority through its actual addressed state carrier.
    await sendFrames(app.socket, [{ type: 'session_state', session_id: MAIN_ID, user_id: 'F5-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' }, session_tokens_total: 47023 }])
    await settleSocketFrame()
    expect(screen.getByTestId('session-token-counter'), 'positive control: separate main has real recorded47023').toHaveAttribute('aria-label', '47023 tokens used')
    const identity: Session = { ...session(TARGET), workspace_id: WS, title: TITLE,
      stats: { ...session(TARGET).stats, tokens_total: legacy, message_count: 1 },
    }
    const main: Session = { ...session(MAIN_ID), workspace_id: WS, type: 'main', stats: { ...session(MAIN_ID).stats, tokens_total: 47023 } }
    const list: SessionPage = { sessions: [main, identity] }
    expect(SessionPageSchema.safeParse(list).success, 'required legacy stats remain a valid numeric wire DTO, not a fabricated missing-stats object').toBe(true)
    app.network.respond('GET', '/api/v1/sessions', () => json(list))
    const unknown = page([entry(901)], { sessionId: TARGET })
    expectValidPage(unknown, [pos(901)], 1)
    expect(unknown, 'this current authority carrier truthfully omits its unknown baseline').not.toHaveProperty('session_tokens_total')
    app.network.respond('GET', `/api/v1/sessions/${TARGET}/messages`, () => json(unknown))
    const detail = deferred<Response>()
    app.network.respond('GET', `/api/v1/sessions/${TARGET}`, () => detail.promise)
    finishDetail = () => detail.resolve(json(availableDetail(unknown, identity)))
    expect(useChatStore.getState().sessionsById[TARGET]?.sessionTokens, 'cold target has no previously established numeric authority').toBeUndefined()

    await user.click(screen.getByRole('button', { name: /^Search sessions$/u }))
    const open = await screen.findByRole('button', { name: `Open ${TITLE}` })
    expect(app.network.requests.some(read => read.path === '/api/v1/sessions' && new URLSearchParams(read.query).get('flat') === 'true'), 'positive instrument: real flat list HTTP fetched and passed through rawToSession').toBe(true)
    await user.click(open)
    await waitFor(() => expect(useSessionStore.getState().activeSessionId, 'actual list selection commits the target session').toBe(TARGET))
    expect(app.socket.sent.filter(frame => frame.type === 'attach_session' && frame.session_id === TARGET && !frame.ack_attention), 'actual useSelectSession emits one real target attach').toHaveLength(1)
    expectUnavailable('after actual list selection, before new recorded authority')

    // This valid snapshot's state and replay done both omit the optional
    // session total. They cannot certify the legacy list number as authoritative.
    await sendSnapshot(app.socket, unknown, 'unknown_position', 200)
    expect(useChatStore.getState().sessionsById[TARGET].history.readerPositions, 'actual frame owner accepted the otherwise valid unknown snapshot').toStrictEqual([pos(901)])
    expectUnavailable('after unknown snapshot state and replay done')
    await act(async () => useChatStore.getState().loadLatestHistory(TARGET))
    expect(app.network.requests.filter(read => read.path === `/api/v1/sessions/${TARGET}/messages`), 'actual unknown REST page was read, not only configured').toHaveLength(1)
    expectUnavailable('after actual unknown REST page')

    const zero: SessionStateFrame = { type: 'session_state', session_id: TARGET, user_id: 'F5-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' }, session_tokens_total: 0 }
    await sendFrames(app.socket, [zero])
    await settleSocketFrame()
    expect.soft(screen.getByTestId('session-token-counter'), 'an actual recorded0 must win over untrusted legacy list metadata').toHaveAttribute('aria-label', '0 tokens used')
    expect.soft(screen.getByTestId('session-token-value'), 'known0 is not the unknown state').toHaveTextContent(/^0 tokens$/u)
    const recorded = { ...page([entry(902)], { sessionId: TARGET }), session_tokens_total: 46323 }
    app.network.respond('GET', `/api/v1/sessions/${TARGET}/messages`, () => json(recorded))
    await act(async () => useChatStore.getState().loadLatestHistory(TARGET))
    await screen.findByText('Saved row 902', { exact: true })
    expect(screen.getByTestId('session-token-counter'), 'real recorded page establishes the same absolute46323 as live').toHaveAttribute('aria-label', '46323 tokens used')
    expect(useChatStore.getState().sessionsById[MAIN_ID].sessionTokens, 'target selection/state/page never contaminates the separately accounted main').toBe(47023)
  })
})
