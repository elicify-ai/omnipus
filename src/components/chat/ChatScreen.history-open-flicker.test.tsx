// #1452 F-FLICKER / Q-T12. Oracle: founder's open-chat ruling (2026-10-27)
// recorded in the squad-1452 coordination ledger; paging spec D7/D8.
// Adapted from the frontend owner's archived A/B reproduction, NOT its output.
// REAL app/router/AssistantUI/Markdown/typewriter/virtualizer/stores. Only
// HTTP, WebSocket and browser geometry/frame delivery are process edges.
// jsdom observes DOM commits and the rendered visibility class, not painted
// pixels, real font metrics or helper-session navigation. Those need browser UAT.
import { afterEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { MAIN_ID } from '../../../tests/l1-room-ui/harness'
import {
  mountStage1App, resetStage1App, settledMainChat, Stage1Network, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'
import {
  deferred, entry, expectValidPage, page, replay, snapshot, STAMP, UNAVAILABLE,
} from '../../../tests/chat-history-paging/fixtures'
import { sendFrames } from '../../../tests/chat-history-paging/application'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let layout: ReturnType<typeof installHistoryLayout> | undefined
let network: Stage1Network | undefined
const observers: MutationObserver[] = []
afterEach(async () => {
  observers.splice(0).forEach(observer => observer.disconnect())
  await stopStage1App()
  layout?.restore()
  layout = undefined
  expect(network?.unexpected, 'all physical HTTP reads stay at declared process edges').toStrictEqual([])
  network = undefined
})

// Authored wire input. The prefix must already be present at the FIRST reveal;
// ordinary streaming may pace later additions, but never re-type this prefix.
const PREFIX = 'PARTIAL one'
const LIVE_ID = 'flicker-running-message'
const liveToken = (content: string, seq?: number): ServerFrame => ({
  type: 'token', session_id: MAIN_ID, content, turn_id: 'turn-1',
  message_id: LIVE_ID, agent_id: 'mia', ...(seq === undefined ? { replace: true } : { seq }),
})
const completion: ServerFrame = {
  type: 'catch_up_complete', session_id: MAIN_ID, boot_id: 'history-boot', seq: 100, mode: 'snapshot',
}

async function openCase() {
  layout = installHistoryLayout({ virtual: true })
  resetStage1App()
  act(() => useSessionStore.setState({ activeSessionId: null }))
  network = new Stage1Network()
  const competingRest = deferred<Response>()
  // WS is the opening authority (D6/D7). Hold a competing disconnected read;
  // never let an empty REST response fabricate readiness or hide the defect.
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => competingRest.promise)
  const app = await mountStage1App(undefined, { initialAttachResponse: 'caller' })
  await settledMainChat(app.socket, { initialAttachResponse: 'caller' })
  expect(app.socket.sent.filter(frame => frame.type === 'attach_session' && frame.session_id === MAIN_ID),
    'instrument: the real route issued exactly one session attach').toHaveLength(1)
  return { ...app, region: screen.getByRole('region', { name: 'Conversation history' }) }
}

function runningPage(withSavedStep: boolean) {
  const saved = [
    entry(1, { role: 'user', content: 'TASK_QUESTION' }),
    entry(2, { role: 'assistant', content: 'EARLIER_ANSWER', turn_id: 'turn-0' }),
    entry(3, { role: 'user', content: 'SECOND_QUESTION' }),
    ...(withSavedStep ? [entry(4, { role: 'assistant', content: 'PARTIAL saved step', turn_id: 'turn-1' })] : []),
  ]
  const history = page(saved, { sessionId: MAIN_ID })
  expectValidPage(history, saved.map(source => source.history_position!), saved.length)
  const frames: ServerFrame[] = [
    snapshot(history),
    { type: 'session_state', session_id: MAIN_ID, user_id: 'flicker-reader',
      history_state: { status: 'available' }, pending_approvals: [], emitted_at: STAMP,
      active_turn: { turn_id: 'turn-1', agent_id: 'mia', started_at: STAMP } },
    ...saved.map(source => replay(source, MAIN_ID)),
    { type: 'done', session_id: MAIN_ID },
    liveToken(PREFIX),
  ]
  return frames
}

function observeDom(record: () => void) {
  const observer = new MutationObserver(record)
  observer.observe(document.body, {
    subtree: true, childList: true, characterData: true, attributes: true,
    attributeFilter: ['class', 'hidden', 'aria-hidden'],
  })
  observers.push(observer)
  record()
}
const covered = (region: HTMLElement) => region.classList.contains('invisible')
const liveRows = (region: HTMLElement) => [...region.querySelectorAll<HTMLElement>('[data-testid="streaming-message-anchor"]')]
async function settleRows() {
  await act(async () => { layout?.flushResize() })
}

// No source-alias/D1 assertion: the saved same-turn step is a distinct message.
// Both classes use the shared renderer that a running helper chat also uses.
describe('F-FLICKER: opening an already-running conversation', () => {
  for (const withSavedStep of [false, true]) {
    it(`never exposes an empty/re-typed live reply ${withSavedStep ? 'after a saved same-turn step' : 'without a saved same-turn step'}`, async () => {
      const { socket, region } = await openCase()
      const shown: string[] = []
      observeDom(() => {
        const rows = liveRows(region)
        if (!covered(region) && rows.length) shown.push(rows[0].textContent ?? '')
      })
      await sendFrames(socket, [...runningPage(withSavedStep), completion])
      await settleRows()
      await waitFor(() => {
        expect(covered(region), 'the completed attach eventually reveals the conversation').toBe(false)
        expect(liveRows(region), 'instrument: a REAL live AssistantUI row is mounted').toHaveLength(1)
      })
      for (let index = 0; index < 3; index++) {
        await sendFrames(socket, [liveToken(` t${index}`, 101 + index)])
        await settleRows()
      }
      const finalText = `${PREFIX} t0 t1 t2`
      await waitFor(() => expect(within(liveRows(region)[0]).getAllByText(finalText, { exact: true }),
        'the running renderer eventually contains the exact authored text once').toHaveLength(1))
      expect(useChatStore.getState().isStreaming, 'instrument: this is still an active reply, not finalized history').toBe(true)
      expect(shown.length, 'instrument: at least one shown DOM observation was captured').toBeGreaterThan(0)
      // Check EVERY visible observation, not just the final settled DOM. A
      // final-text-only assertion misses the original blank-and-retype bug.
      for (const text of shown) expect(text,
        'a shown frame never re-types the in-progress catch-up reply from empty').toContain(PREFIX)
      expect(liveRows(region), 'streaming does not duplicate the current bubble').toHaveLength(1)
      expect(screen.queryByTestId('chat-open-cover'), 'ordinary streaming never restores the opening cover').toBeNull()
    })
  }

  it('keeps one loading cover until live catch-up completes, then reveals only once', async () => {
    const { socket, region } = await openCase()
    const states: boolean[] = []
    observeDom(() => {
      const current = covered(region)
      if (states.at(-1) !== current) states.push(current)
    })
    expect(screen.getByTestId('chat-open-cover'), 'an unready open has the minimal loading surface').toBeInTheDocument()
    expect(states, 'the thread is covered before any history arrives').toStrictEqual([true])
    expect(screen.getAllByRole('status').filter(node => node.textContent === 'Loading messages…'),
      'D8: one polite opening status, never competing announcements').toHaveLength(1)

    // Deliver the page, replay and current projection, but withhold the actual
    // transport completion. The real render-once socket buffer stages these
    // rows until completion; no fixture may bypass it to fabricate readiness.
    await sendFrames(socket, runningPage(false))
    await settleRows()
    expect(covered(region), 'delivered page data cannot reveal before catch_up_complete').toBe(true)
    expect(useChatStore.getState().sessionsById[MAIN_ID]?.awaitingCatchUp,
      'instrument: catch-up is genuinely still pending').toBe(true)

    await sendFrames(socket, [completion])
    await settleRows()
    await waitFor(() => expect(screen.queryByTestId('chat-open-cover'), 'completion/layout clears the cover').toBeNull())
    expect(liveRows(region), 'instrument: completion renders the real live row').toHaveLength(1)
    expect(region.querySelector('[data-history-position]')?.getBoundingClientRect().height,
      'instrument: revealed rows have nonzero browser-edge geometry').toBeGreaterThan(0)
    expect(within(liveRows(region)[0]).getByText(PREFIX, { exact: true }),
      'the FIRST settled reveal already contains the entire catch-up prefix').toBeInTheDocument()
    await sendFrames(socket, [liveToken(' still running', 101)])
    await settleRows()
    await waitFor(() => expect(within(liveRows(region)[0]).getByText(`${PREFIX} still running`, { exact: true })).toBeInTheDocument())
    expect(states, 'one covered -> revealed transition; live tokens never re-cover').toStrictEqual([true, false])
  })

  it('reveals the existing unavailable-history error and Retry instead of trapping the reader under a cover', async () => {
    const { socket, region } = await openCase()
    expect(covered(region), 'instrument: this open really begins covered').toBe(true)
    await sendFrames(socket, [
      { type: 'session_state', session_id: MAIN_ID, user_id: 'flicker-reader', pending_approvals: [], emitted_at: STAMP,
        history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE } },
      { ...completion, mode: 'live_only' },
    ])
    await settleRows()
    await waitFor(() => expect(covered(region), 'failure must reveal recovery, not keep spinning').toBe(false))
    expect(screen.queryByTestId('chat-open-cover'), 'there is no endless opening spinner after a declared failure').toBeNull()
    const failure = screen.getByRole('alert')
    expect(failure, 'D8: the fixed history failure is shown, not a fabricated empty conversation').toHaveTextContent(UNAVAILABLE)
    const retry = within(failure).getByRole('button', { name: 'Retry' })
    expect(retry, 'the existing recovery action remains reachable').toBeEnabled()
    expect(retry).not.toHaveAttribute('aria-disabled', 'true')
    expect(screen.queryAllByRole('status').filter(node => node.textContent === 'Loading messages…'),
      'opening loading status clears on failure').toStrictEqual([])
  })
})
