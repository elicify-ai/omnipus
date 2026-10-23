// Silent-failure F8: visible snapshot rejection and sanitized diagnostic cause.
// Real app/parser/frame owner/merger/ErrorState/Retry and production telemetry.
// Only HTTP/socket inputs, environment and console output are process edges.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { TelemetryEvent } from '@/lib/telemetry'
import { logDiagnostic, _resetTelemetryForTest } from '@/lib/telemetry'
import { parseFrameSafe } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { entry, page, pos, replay, snapshot, STAMP } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, sendFrames, stopHistory } from '../../../tests/chat-history-paging/application'

const BODY = 'F8_PRIVATE_BODY_DO_NOT_LOG'
const PRIVATE_ERROR = 'F8_PRIVATE_PROGRAMMING_DETAIL_DO_NOT_LOG'
const POSITION = pos(901)
let app: Awaited<ReturnType<typeof openHistory>> | undefined
let unsubscribe: (() => void) | undefined

afterEach(async () => {
  unsubscribe?.(); unsubscribe = undefined
  await stopHistory(app); app = undefined
  vi.unstubAllEnvs()
  vi.restoreAllMocks()
  _resetTelemetryForTest()
})

type ConsoleSink = ReturnType<typeof observeProductionDiagnostics> // not-wire-format
function records(sink: ConsoleSink): TelemetryEvent[] {
  return sink.mock.calls.flatMap(args => {
    const line = args[0]
    // Non-diagnostic React/browser console lines are not this structured sink.
    // A malformed telemetry JSON line must throw, never silently disappear.
    return typeof line === 'string' && line.startsWith('{"telemetry":"error"')
      ? [JSON.parse(line) as TelemetryEvent] : []
  })
}
function observeProductionDiagnostics() {
  vi.stubEnv('DEV', false)
  vi.stubEnv('MODE', 'production')
  _resetTelemetryForTest()
  const sink = vi.spyOn(console, 'error') // Call through: no log suppression.
  logDiagnostic('F8.instrument', { phase: 'instrument', kind: 'control' })
  const emitted = sink.mock.calls.flatMap(args => typeof args[0] === 'string' && args[0].startsWith('{"telemetry":"error"') ? [JSON.parse(args[0])] : [])
  expect(emitted, 'positive instrument: real production logger reaches the actual console sink').toStrictEqual([
    { telemetry: 'error', event: 'F8.instrument', phase: 'instrument', kind: 'control' },
  ])
  sink.mockClear()
  return sink
}
function phaseText(record: TelemetryEvent) {
  // The brief specifies meaning, not a new exact event name/private helper API.
  return Object.entries(record).filter(([key]) => /event|phase|stage|operation|transport/iu.test(key)).map(([, value]) => String(value)).join(' ')
}
function failureKind(record: TelemetryEvent) {
  return Object.entries(record).filter(([key]) => /event|kind|reason|code|classif|exception.?type/iu.test(key)).map(([, value]) => String(value)).join(' ')
}
function snapshotPhase(record: TelemetryEvent) {
  const text = phaseText(record)
  return /snapshot/iu.test(text) && /commit|finish|reconstruct|validat|projection/iu.test(text)
}
function expectPrivateDataAbsent(events: TelemetryEvent[]) {
  const serialized = JSON.stringify(events)
  expect(serialized, 'diagnostic cannot contain source bodies').not.toContain(BODY)
  expect(serialized, 'diagnostic cannot contain opaque source positions').not.toContain(POSITION)
  expect(serialized, 'unexpected exception messages must not become diagnostic data').not.toContain(PRIVATE_ERROR)
}

async function startReader() {
  app = await openHistory(page(Array.from({ length: 40 }, (_, index) => entry(501 + index)), { sessionId: MAIN_ID }))
  expect(useChatStore.getState().history.readerPositions, 'positive control: a valid prior snapshot really committed').toStrictEqual(Array.from({ length: 40 }, (_, index) => pos(501 + index)))
  return screen.getByRole('region', { name: 'Conversation history' })
}
function snapshotFrames(history: SessionHistoryResponse, advertisedRows = 1): ServerFrame[] {
  return [
    { ...snapshot(history), seq: 200, page_info: { ...history.page_info, row_count: advertisedRows } },
    { type: 'session_state', session_id: MAIN_ID, user_id: 'F8-reader', history_state: { status: 'available' }, pending_approvals: [], emitted_at: STAMP },
    ...history.entries.map(source => replay(source, MAIN_ID)),
    { type: 'done', session_id: MAIN_ID },
    { type: 'catch_up_complete', session_id: MAIN_ID, boot_id: 'history-boot', seq: 200, mode: 'snapshot' },
  ]
}
async function deliverActualSnapshot(history: SessionHistoryResponse, advertisedRows = 1) {
  await act(async () => {
    expect(useConnectionStore.getState().connection?.send({ type: 'attach_session', session_id: MAIN_ID }), 'positive transport: an actual attach requests the replacement').toBe(true)
  })
  await sendFrames(app!.socket, snapshotFrames(history, advertisedRows))
  // Actual WsConnection dispatches its bounded snapshot batch on a browser frame.
  await act(async () => new Promise<void>(resolve => requestAnimationFrame(() => resolve())))
}

describe('F8 snapshot commit failure classification and visible recovery', () => {
  it('accepts matching reconstructed row count without inventing a failure diagnostic', async () => {
    const region = await startReader()
    const sink = observeProductionDiagnostics()
    const valid = page([entry(901, { content: BODY })], { sessionId: MAIN_ID })
    await deliverActualSnapshot(valid)
    await screen.findByText(BODY, { exact: true })
    expect(useChatStore.getState().history.readerPositions).toStrictEqual([POSITION])
    expect(useChatStore.getState().history.historyLoaded).toBe(true)
    expect(within(region).queryByRole('alert'), 'matching count is not a rejection').not.toBeInTheDocument()
    expect(records(sink).filter(snapshotPhase), 'working production sink records no false failure').toStrictEqual([])
  })

  it('rejects a structurally valid2-versus1 row contradiction visibly with Retry and a sanitized phase/failure diagnostic', async () => {
    const user = userEvent.setup()
    const region = await startReader()
    const prior = useChatStore.getState().history.readerPositions.slice()
    const sink = observeProductionDiagnostics()
    const oneRow = page([entry(901, { content: BODY })], { sessionId: MAIN_ID })
    expect(oneRow.page_info.row_count, 'independent fixture: one narration, no tool children').toBe(1)
    const frames = snapshotFrames(oneRow, 2)
    expect(frames.map(frame => parseFrameSafe(frame) !== null), 'all carriers are structurally legal; only reconstructed accounting contradicts metadata').toStrictEqual(Array(frames.length).fill(true))
    await deliverActualSnapshot(oneRow, 2)
    const alert = await within(region).findByRole('alert')
    expect(alert, 'existing visible rejection is preserved').toHaveTextContent('Could not load messages.')
    expect(useChatStore.getState().history.historyLoaded).toBe(false)
    expect(useChatStore.getState().history.readerPositions, 'bad snapshot cannot replace the prior reader').toStrictEqual(prior)
    expect(within(region).queryByText(BODY, { exact: true }), 'rejected source body is never published').not.toBeInTheDocument()
    const diagnostic = records(sink).filter(record => snapshotPhase(record) && /row[_ -]?count/iu.test(failureKind(record)))
    expect.soft(diagnostic, 'F8 requires a retained sanitized snapshot phase AND row-count failure kind, not an unbound catch').toHaveLength(1)
    expectPrivateDataAbsent(records(sink))

    // Keep recovery assertions executing even while the missing diagnostic is RED.
    const retry = within(alert).getByRole('button', { name: /^Retry$/u })
    app!.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(oneRow))
    const reads = app!.network.requests.filter(read => read.path === `/api/v1/sessions/${MAIN_ID}/messages`).length
    await user.click(retry)
    await screen.findByText(BODY, { exact: true })
    await waitFor(() => expect(useChatStore.getState().history.historyLoaded).toBe(true))
    expect(useChatStore.getState().history.readerPositions, 'actual bounded Retry commits the valid source').toStrictEqual([POSITION])
    expect(app!.network.requests.filter(read => read.path === `/api/v1/sessions/${MAIN_ID}/messages`), 'one real Retry request, no resend').toHaveLength(reads + 1)
    expect(within(region).queryByRole('alert'), 'success clears the existing error surface').not.toBeInTheDocument()
    expectPrivateDataAbsent(records(sink))
  })

  it('does not misclassify an unexpected TypeError from a real commit consumer as protocol/row-count failure', async () => {
    await startReader()
    const sink = observeProductionDiagnostics()
    const valid = page([entry(901, { content: BODY })], { sessionId: MAIN_ID })
    const injected = new TypeError(`${PRIVATE_ERROR} ${BODY} ${POSITION}`)
    let reached = 0
    // A real PUBLIC subscriber is an external consumer of the actual store,
    // not a mocked merger, reducer, projection function or internal context.
    // Prior cached history prevents progressive publication: this fires only
    // after a structurally/accounting-valid replacement reaches the commit.
    unsubscribe = useChatStore.subscribe((state, previous) => {
      if (reached === 0 && state.sessionsById[MAIN_ID]?.history.readerPositions[0] === POSITION && previous.sessionsById[MAIN_ID]?.history.readerPositions[0] !== POSITION) {
        reached++
        throw injected
      }
    })
    const parsed = snapshotFrames(valid).map(frame => {
      const accepted = parseFrameSafe(frame)
      expect(accepted, 'fault probe uses only valid declared frames').not.toBeNull()
      expect(accepted?.type, 'real parser accepts the exact server discriminator').toBe(frame.type)
      // parseFrameSafe also admits client WsFrame variants; the original input
      // is already generated ServerFrame, validated above, with no unsafe cast.
      return frame
    })
    let propagated: unknown
    try {
      act(() => parsed.forEach(frame => useChatStore.getState().handleFrame(frame)))
    } catch (error) {
      propagated = error // Retained and asserted below, never swallowed as success.
    }
    unsubscribe(); unsubscribe = undefined
    expect(reached, 'positive fault instrument: actual valid commit reached the external consumer exactly once').toBe(1)
    const snapshotDiagnostics = records(sink).filter(snapshotPhase)
    expect(snapshotDiagnostics.filter(record => /protocol|row[_ -]?count|validation/iu.test(failureKind(record))), 'unexpected TypeError is not a known protocol/row-count refusal').toStrictEqual([])
    if (propagated !== undefined) {
      expect(propagated, 'transparent propagation must preserve the exact unexpected TypeError, not wrap it as a protocol error').toBe(injected)
      expect(propagated).toBeInstanceOf(TypeError)
    } else {
      const unexpected = snapshotDiagnostics.filter(record => /unexpected|internal|programming|type.?error/iu.test(failureKind(record)))
      expect(unexpected, 'a caught unexpected exception needs a truthful sanitized classification, not generic protocol rejection with no cause').toHaveLength(1)
    }
    expectPrivateDataAbsent(records(sink))
  })
})
