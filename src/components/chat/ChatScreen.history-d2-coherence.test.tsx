// T66/B78: full-content cache generations are authoritative-commit based,
// never equality of preview/size/status. Actual application/store/query cache.
import { afterEach, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { deferred, page, replay, snapshot, STAMP } from '../../../tests/chat-history-paging/fixtures'
import { sendFrames, stopHistory } from '../../../tests/chat-history-paging/application'
import { FULL_SOURCE, FULL_TEXT, MAIN_ID, MARKER, fullPath, fullReads, json, openPreviewApp, pos, previewToggle, replaceRows, toolSource, type PreviewApp } from '../../../tests/chat-history-paging/d2-preview-harness'

let app: PreviewApp | undefined
const releases: (() => void)[] = []
afterEach(async () => { releases.splice(0).forEach(fn => fn()); await stopHistory(app); app = undefined; vi.unstubAllGlobals() })

async function redeliver(kind: 'page' | 'snapshot' | 'refresh') {
  if (kind !== 'snapshot') { await replaceRows(app!, [toolSource()], kind === 'refresh' ? 'window_refresh' : 'latest'); return }
  const source = toolSource()
  const selected = page([source], { sessionId: MAIN_ID, rows: 1, info: { limit: 20, min_rows: 1 } })
  useConnectionStore.getState().connection?.send({ type: 'attach_session', session_id: MAIN_ID })
  await sendFrames(app!.socket, [
    { ...snapshot(selected), seq: 200, boot_id: 'history-boot' },
    { type: 'session_state', session_id: MAIN_ID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' } },
    replay(source, MAIN_ID),
    { type: 'tool_call_start', session_id: MAIN_ID, call_id: source.tool_calls![0].id, tool: 'read_file', params: { path: 'fixture.txt' }, history_origin: { position: pos(1), entry_id: source.id } },
    { type: 'tool_call_result', session_id: MAIN_ID, call_id: source.tool_calls![0].id, tool: 'read_file', status: 'success', result: MARKER, history_origin: { position: pos(1), entry_id: source.id } },
    { type: 'done', session_id: MAIN_ID },
    { type: 'catch_up_complete', session_id: MAIN_ID, seq: 200, boot_id: 'history-boot', mode: 'snapshot' },
  ])
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.cursor?.seq).toBe(200))
}
for (const kind of ['page', 'snapshot', 'refresh'] as const) {
  it(`T66: ${kind} with equal preview/size/status invalidates and refetches an expanded corrected suffix`, async () => {
    app = await openPreviewApp()
    const corrected = FULL_TEXT.replace('-FULL_END', '-NEW_END!')
    expect(corrected.length).toBe(FULL_TEXT.length)
    expect(JSON.stringify({ content: corrected }).slice(0, 4096)).toBe(MARKER.preview)
    app.network.respond('GET', fullPath(), () => json(FULL_SOURCE))
    await userEvent.setup().click(previewToggle())
    await waitFor(() => expect(document.body.textContent).toContain('-FULL_END'))
    expect(fullReads(app)).toHaveLength(1)
    expect(JSON.stringify(queryClient.getQueryCache().getAll().map(query => query.state.data)), 'instrument: successful full content really entered the application query cache').toContain(FULL_TEXT)
    const fresh = deferred<Response>(); releases.push(() => fresh.resolve(json(FULL_SOURCE)))
    app.network.respond('GET', fullPath(), () => fresh.promise)
    await redeliver(kind)
    await waitFor(() => expect(fullReads(app!)).toHaveLength(2))
    expect(document.body.textContent, 'expanded item must fall back to preview while new generation loads').not.toContain('-FULL_END')
    expect(JSON.stringify(queryClient.getQueryCache().getAll().map(query => query.state.data)), 'equal-signature authoritative commit drops the prior cached body immediately').not.toContain(FULL_TEXT)
    await act(async () => fresh.resolve(json({ ...FULL_SOURCE, tool_calls: [{ ...FULL_SOURCE.tool_calls![0], result: { content: corrected } }] })))
    await waitFor(() => expect(document.body.textContent).toContain('-NEW_END!'))
    expect(fullReads(app)).toHaveLength(2)
    expect(JSON.stringify(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById)).not.toContain(corrected)
  })
}

it('T66: refresh while full read is pending discards the old same-session response and coalesces one new-generation read', async () => {
  app = await openPreviewApp()
  const old = deferred<Response>(), fresh = deferred<Response>()
  releases.push(() => old.resolve(json(FULL_SOURCE)), () => fresh.resolve(json(FULL_SOURCE)))
  let reads = 0
  app.network.respond('GET', fullPath(), () => ++reads === 1 ? old.promise : fresh.promise)
  await userEvent.setup().click(previewToggle())
  await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
  await redeliver('refresh')
  await waitFor(() => expect(fullReads(app!)).toHaveLength(2))
  await act(async () => old.resolve(json(FULL_SOURCE)))
  expect(document.body.textContent, 'old response cannot appear, populate cache or replace new loading state').not.toContain('-FULL_END')
  expect(JSON.stringify(queryClient.getQueryCache().getAll().map(query => query.state.data))).not.toContain(FULL_TEXT)
  const corrected = FULL_TEXT.replace('-FULL_END', '-NEW_END!')
  await act(async () => fresh.resolve(json({ ...FULL_SOURCE, tool_calls: [{ ...FULL_SOURCE.tool_calls![0], result: { content: corrected } }] })))
  await waitFor(() => expect(document.body.textContent).toContain('-NEW_END!'))
  expect(fullReads(app)).toHaveLength(2)
})

it.each(['success', 'failure'] as const)('T66: leaving aborts full read; late %s cannot repopulate cache or show a stale error on return', async outcome => {
  app = await openPreviewApp()
  const held = deferred<Response>(); releases.push(() => held.resolve(json(FULL_SOURCE)))
  const signals: AbortSignal[] = []
  const originalFetch = globalThis.fetch
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith(fullPath())) { if (init?.signal) signals.push(init.signal); return held.promise }
    return originalFetch(input, init)
  })
  await userEvent.setup().click(previewToggle())
  await waitFor(() => expect(signals).toHaveLength(1))
  await act(async () => {
    useChatStore.getState().releaseSessionHistory(MAIN_ID)
    useSessionStore.getState().setActiveSession('d2-other-session', 'mia')
  })
  expect(signals[0].aborted, 'actual request signal must be aborted on release').toBe(true)
  await act(async () => {
    if (outcome === 'failure') held.reject(new TypeError('late released transport failure'))
    else held.resolve(json(FULL_SOURCE))
  })
  expect(JSON.stringify(queryClient.getQueryCache().getAll().map(query => query.state.data))).not.toContain(FULL_TEXT)
  expect(JSON.stringify(useChatStore.getState().sessionsById[MAIN_ID])).not.toContain(FULL_TEXT)
  vi.stubGlobal('fetch', originalFetch)
  await act(async () => useSessionStore.getState().setActiveSession(MAIN_ID, 'mia'))
  await replaceRows(app, [toolSource()])
  app.network.respond('GET', fullPath(), () => json(FULL_SOURCE))
  await userEvent.setup().click(previewToggle())
  await waitFor(() => expect(document.body.textContent).toContain('-FULL_END'))
  expect(screen.queryByRole('alert'), 'a late old-session failure never becomes a stale error on the next visit').not.toBeInTheDocument()
  expect(fullReads(app), 'next visit uses a fresh request instead of late released content').toHaveLength(1)
})
