// Q-T2 — the HOOK side of the fill-rejection finding (#1452; #1266 final gate
// fg2-scsfh-v2 F1 "void requestFill turns the store's designed-loud TypeError
// channel into a silent, unrecoverable wedge").
//
// The store side already landed (6d56c038a: a rejected read writes the
// action's error copy and its Retry instead of staying 'loading'). What is
// still missing is the HOOK side: useHistoryViewport.ts::runHistoryFill awaits
// the store action with no catch, and every call site discards the returned
// promise with `void requestFill(...)`. So the store's deliberately-loud
// TypeError re-throw escapes the fill as an UNHANDLED REJECTION: nothing in
// the product records it, and the failure is invisible to the operator.
//
// Oracles (independent of this product's implementation):
//  - The rejection must be caught by the hook and recorded LOCALLY through
//    logDiagnostic — the issue's own wording: "useHistoryViewport.ts::
//    runHistoryFill / requestFill call sites should .catch and log locally
//    via logDiagnostic".
//  - The record must classify the failure without leaking anything (spec
//    diagnostics rule, spec:203 and spec:252 — "New logs have no
//    text/result/parameter/raw-query canaries"; a history POSITION is never
//    a public diagnostic).
//  - The escaped rejection must not reach the process as an unhandled
//    rejection (that is the defect's mechanism).
//
// Real list/store/adapter/hook; only HTTP, socket and browser geometry are
// edges. The fault is injected at the process edge (a Response whose
// `headers` accessor throws), NOT by mocking a hook, the store or the API.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import * as telemetry from '@/lib/telemetry'
import { useChatStore } from '@/store/chat'
import { deferred } from '../../../tests/chat-history-paging/fixtures'
import {
  resetScrollCase, stopScrollCase, settleScrollLayout, mountRetained, loadAndRender,
  establishLatest, MAIN_ID, entry, page, pos, expectValidPage, json,
} from '../../../tests/chat-history-paging/scroll-regression-harness'

let env: ReturnType<typeof resetScrollCase>
beforeEach(() => { env = resetScrollCase() })
afterEach(async () => {
  await stopScrollCase(env.layout)
  expect(env.network.unexpected, 'no undeclared physical HTTP reads').toStrictEqual([])
})

it('Q-T2: a rejected history fill is caught by the hook, logged locally, and never escapes as an unhandled rejection', async () => {
  const diagnostic = vi.spyOn(telemetry, 'logDiagnostic')
  const unhandled: unknown[] = []
  const onUnhandled = (reason: unknown) => { unhandled.push(reason) }
  process.on('unhandledRejection', onUnhandled)
  try {
    const { selected } = await mountRetained(40, true)
    const region = await loadAndRender(env.network, selected)
    await establishLatest(region, env.layout)
    expect(useChatStore.getState().history.viewMode, 'the reader really follows latest before the failing fill').toBe('latest')

    // A genuine older page, so the fill that follows is a REAL read.
    const olderSources = Array.from({ length: 20 }, (_, i) => entry(i + 1, { id: `q-t2-older-${i + 1}`, role: 'assistant', content: `Q-T2 older source ${i + 1}` }))
    const older = page(olderSources, { sessionId: MAIN_ID, earlier: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(older, olderSources.map(row => row.history_position!), 20)

    // Process-edge fault: the response's `headers` accessor throws a raw
    // TypeError AFTER the response was produced. Ordinary rejected fetches
    // are wrapped as ApiError by the transport, so this is the designed-loud
    // channel the store deliberately re-throws.
    const pendingResponse = deferred<Response>()
    let olderRequests = 0
    let faultReads = 0
    const fault = new TypeError('Q-T2 synthetic response-handling fault')
    env.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, request => {
      const query = new URLSearchParams(request.query)
      if (!query.has('before')) return json(selected)
      olderRequests++
      expect(query.get('before'), 'the fill asks for the position of the reader\'s oldest held row').toBe(pos(21))
      return pendingResponse.promise
    })

    await act(async () => {
      fireEvent.wheel(region, { deltaY: -400 })
      region.scrollTop = 0
      fireEvent.scroll(region)
    })
    await waitFor(() => expect(olderRequests, 'the actual viewport fill started its real store read').toBe(1))

    const brokenResponse = json(older)
    Object.defineProperty(brokenResponse, 'headers', { get() { faultReads++; throw fault } })
    await act(async () => { pendingResponse.resolve(brokenResponse); await Promise.resolve() })
    await waitFor(() => expect(faultReads, 'the raw response-access fault really reached the real adapter').toBeGreaterThan(0))

    // The store's landed half: the failure is VISIBLE with a working Retry.
    await waitFor(() => expect(useChatStore.getState().history.requests.older.status,
      'D8: the rejected fill must not leave the direction permanently loading').toBe('error'))
    expect(screen.getByRole('alert'), 'the failed fill is visible to the reader').toBeVisible()
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load earlier messages.')

    // The HOOK half — the missing piece. The rejection is recorded locally.
    await waitFor(() => expect(diagnostic.mock.calls.length,
      'the hook must record the escaped fill rejection through logDiagnostic').toBeGreaterThan(0))
    expect(diagnostic.mock.calls.length, 'the rejected fill is recorded exactly once, not per call site').toBe(1)
    // The event name and field shape are the repo's own convention for a
    // rejected history read (src/store/chat/history-replay.ts records
    // 'chatHistorySnapshotRejected' / 'chatHistorySnapshotRestoreFailed' the
    // same way), and the FE lane landed this one as
    // 'chatHistoryReadRejected' with { action, error }.
    const [event, fields] = diagnostic.mock.calls[0] as [string, Record<string, unknown>]
    expect(event, 'the diagnostic names the rejected history read').toBe('chatHistoryReadRejected')
    expect(fields, 'the record classifies which read failed').toMatchObject({ action: 'older' })
    expect(fields, 'the record classifies the failure kind, not the cause').toMatchObject({ error: 'TypeError' })

    // The record classifies the failure; it never carries content, a position
    // or the raw cause (spec:203, spec:252, spec:484).
    const logged = JSON.stringify(diagnostic.mock.calls)
    expect(logged, 'no raw error message may reach the diagnostic').not.toContain('Q-T2 synthetic response-handling fault')
    expect(logged, 'no message content may reach the diagnostic').not.toContain('Q-T2 older source')
    expect(logged, 'no history position may reach the diagnostic').not.toContain(pos(1))
    expect(logged, 'no stack may reach the diagnostic').not.toContain('at Object')

    // The defect's mechanism: the rejection must not escape the fill.
    await act(async () => { await new Promise<void>(resolve => setTimeout(resolve, 0)) })
    await settleScrollLayout()
    expect(unhandled, 'the escaped fill rejection must not become an unhandled rejection').toStrictEqual([])
  } finally {
    process.off('unhandledRejection', onUnhandled)
    diagnostic.mockRestore()
  }
})
