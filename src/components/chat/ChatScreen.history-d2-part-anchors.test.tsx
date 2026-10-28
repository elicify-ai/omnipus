// T66 / D1 S9/S10 / B78: actual live tokens -> tools -> tokens -> saved
// preview settlement -> per-part render overlay. No pre-seeded part list.
import { afterEach, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { entry, page, pos, replay, snapshot, STAMP } from '../../../tests/chat-history-paging/fixtures'
import { sendFrames, stopHistory } from '../../../tests/chat-history-paging/application'
import { MAIN_ID, fullPath, json, openPreviewApp, type PreviewApp } from '../../../tests/chat-history-paging/d2-preview-harness'

let app: PreviewApp | undefined
afterEach(async () => { await stopHistory(app); app = undefined })
const TURN = 'd2-part-anchor-turn'
const A = 'd2-live-A', B = 'd2-live-B'
// A has100000 UTF16 units,100002 UTF8 bytes. Euro straddles byte65536:
// collapsed A is65534 ASCII units; B is90000 ASCII units, preview65536.
const FULL_A = 'A_START' + 'a'.repeat(65534 - 7) + '€' + 'a'.repeat(100000 - 65535 - 5) + 'A_END'
const FULL_B = 'B_START' + 'b'.repeat(90000 - 7 - 5) + 'B_END'
const PREVIEW_A = FULL_A.slice(0, 65534), PREVIEW_B = FULL_B.slice(0, 65536)
const call = (id: string, tool: string) => ({ id, tool, status: 'success' as const, content_state: 'full' as const, parameters: {}, result: { text: 'completed' } })
function selected(): Message[] {
  return [entry(1, { id: A, role: 'assistant', agent_id: 'mia', turn_id: TURN, content: PREVIEW_A, content_original_size_bytes: new TextEncoder().encode(FULL_A).length, tool_calls: [call('part-A-tool', 'read_file')] }),
    entry(2, { id: B, role: 'assistant', agent_id: 'mia', turn_id: TURN, content: PREVIEW_B, content_original_size_bytes: FULL_B.length, tool_calls: [call('part-B-tool', 'bash')] })]
}
async function witnessAndSettle(liveWins = false) {
  app = await openPreviewApp([])
  const live: ServerFrame[] = [
    // TokenFrame limits each chunk to65536 characters; the witnessed reply
    // is built from legal chunks, not one oversized malformed test frame.
    { type: 'token', session_id: MAIN_ID, message_id: A, turn_id: TURN, agent_id: 'mia', content: FULL_A.slice(0, 50000) },
    { type: 'token', session_id: MAIN_ID, message_id: A, turn_id: TURN, agent_id: 'mia', content: FULL_A.slice(50000) },
    { type: 'tool_call_start', session_id: MAIN_ID, call_id: 'part-A-tool', tool: 'read_file', params: {} },
    { type: 'tool_call_result', session_id: MAIN_ID, call_id: 'part-A-tool', tool: 'read_file', status: 'success', result: { text: 'completed' } },
    { type: 'token', session_id: MAIN_ID, message_id: B, turn_id: TURN, agent_id: 'mia', content: FULL_B.slice(0, 50000) },
    { type: 'token', session_id: MAIN_ID, message_id: B, turn_id: TURN, agent_id: 'mia', content: FULL_B.slice(50000) },
    { type: 'tool_call_start', session_id: MAIN_ID, call_id: 'part-B-tool', tool: 'bash', params: {} },
    { type: 'tool_call_result', session_id: MAIN_ID, call_id: 'part-B-tool', tool: 'bash', status: 'success', result: { text: 'completed' } },
  ]
  // Separate real streaming phases with event/state barriers; tokens are
  // buffered to a browser frame by WsConnection and must commit before the
  // tool start can stamp its witnessed part-relative anchor.
  await sendFrames(app.socket, live.slice(0, 2))
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById[A]?.content).toBe(FULL_A))
  await sendFrames(app.socket, live.slice(2, 4))
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.toolCalls['part-A-tool']?.status).toBe('success'))
  await sendFrames(app.socket, live.slice(4, 6))
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById[A]?.content, 'instrument: real token/tool joiner and alias').toBe(FULL_A + '\n\n' + FULL_B))
  await sendFrames(app.socket, live.slice(6))
  const sources = selected()
  const p = page(sources, { sessionId: MAIN_ID, rows: 4, info: { limit: 20, min_rows: 1 } })
  useConnectionStore.getState().connection?.send({ type: 'attach_session', session_id: MAIN_ID })
  const frames: ServerFrame[] = [{ ...snapshot(p), seq: 200, boot_id: 'history-boot' }, {
    type: 'session_state', session_id: MAIN_ID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
    history_state: { status: 'available' }, ...(liveWins ? { active_turn: { turn_id: TURN, agent_id: 'mia', started_at: STAMP } } : {}),
  }]
  for (const source of sources) {
    frames.push({ ...replay(source, MAIN_ID), content_original_size_bytes: source.content_original_size_bytes })
    for (const tc of source.tool_calls!) frames.push(
      { type: 'tool_call_start', session_id: MAIN_ID, call_id: tc.id, tool: tc.tool, params: {}, history_origin: { position: source.history_position!, entry_id: source.id } },
      { type: 'tool_call_result', session_id: MAIN_ID, call_id: tc.id, tool: tc.tool, result: tc.result, status: 'success', history_origin: { position: source.history_position!, entry_id: source.id } },
    )
  }
  frames.push({ type: 'done', session_id: MAIN_ID }, { type: 'catch_up_complete', session_id: MAIN_ID, seq: 200, boot_id: 'history-boot', mode: 'snapshot' })
  await sendFrames(app.socket, frames)
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.cursor?.seq).toBe(200))
  return sources
}
function relativeAnchorContent() {
  const b = useChatStore.getState().sessionsById[MAIN_ID]
  const row = b.messagesById[b.historySourceAliases![pos(1)]]
  return { row, offsets: row.tool_calls?.map(tc => Reflect.get(tc, 'textOffset')) }
}
for (const order of [[0, 1], [1, 0]]) {
  it(`T66 S9/S10: expands parts in order${order.join('->')} without double-shifting joiners or tool anchors`, async () => {
    const saved = await witnessAndSettle()
    const { row, offsets } = relativeAnchorContent()
    expect(row.content).toBe(PREVIEW_A + '\n\n' + PREVIEW_B)
    expect(offsets, 'relative A-end and B-end clamp into each own preview').toEqual([65534, 65534 + 2 + 65536])
    for (const [i, full] of [FULL_A, FULL_B].entries()) app!.network.respond('GET', fullPath(pos(i + 1)), () => json({ ...saved[i], content: full, content_original_size_bytes: undefined }))
    const user = userEvent.setup()
    expect(screen.getAllByRole('button', { name: /^Show the rest/i }), 'one control for EACH shortened part, in source order').toHaveLength(2)
    for (const index of order) {
      const controls = screen.getAllByRole('button', { name: /^Show the rest/i })
      // Both controls remain in source order if an expanded control keeps its
      // label; if it disappears, the only remaining control is the other part.
      const control = controls.length === 2 ? controls[index] : controls[0]
      expect(control, 'each shortened part has its own reachable expansion control').toBeDefined()
      await user.click(control)
      await waitFor(() => expect(document.body.textContent).toContain(index === 0 ? 'A_END' : 'B_END'))
    }
    expect(app!.network.requests.filter(r => r.path === fullPath(pos(1)) || r.path === fullPath(pos(2))).map(r => r.path)).toEqual(order.map(i => fullPath(pos(i + 1))))
    expect(relativeAnchorContent().row.content, 'full text is a render overlay, never written to stored preview').toBe(PREVIEW_A + '\n\n' + PREVIEW_B)
    expect(relativeAnchorContent().offsets).toEqual(offsets)
    const storedRow = JSON.stringify(relativeAnchorContent().row)
    expect(storedRow, 'the body-free part list must not become another full-text owner').not.toContain(FULL_A)
    expect(storedRow).not.toContain(FULL_B)
    const host = document.querySelector(`[data-message-id="${row.id}"]`)!
    const walker = document.createTreeWalker(host, NodeFilter.SHOW_TEXT)
    const textNodes: Text[] = []; for (let node = walker.nextNode(); node; node = walker.nextNode()) textNodes.push(node as Text)
    const aEnd = textNodes.find(node => node.textContent?.includes('A_END'))
    const bStart = textNodes.find(node => node.textContent?.includes('B_START'))
    const toggles = host.querySelectorAll('[data-history-tool-row] button[aria-expanded]')
    expect(aEnd).toBeDefined(); expect(bStart).toBeDefined(); expect(toggles).toHaveLength(2)
    expect(aEnd!.compareDocumentPosition(toggles[0]) & Node.DOCUMENT_POSITION_FOLLOWING, 'A tool remains after FULL A').not.toBe(0)
    expect(toggles[0].compareDocumentPosition(bStart!) & Node.DOCUMENT_POSITION_FOLLOWING, 'A tool stays before B, regardless of expansion order').not.toBe(0)
  })
}
it('T66 S2/S10: live-wins part retains witnessed full text/anchors and has no expansion control', async () => {
  await witnessAndSettle(true)
  const { row, offsets } = relativeAnchorContent()
  expect(row.content).toBe(FULL_A + '\n\n' + FULL_B)
  expect(offsets).toEqual([100000, 100000 + 2 + 90000])
  expect(screen.queryByRole('button', { name: /^Show the rest/i })).not.toBeInTheDocument()
  expect(app!.network.requests.filter(r => r.path.startsWith(`/api/v1/sessions/${MAIN_ID}/messages/`))).toEqual([])
})
