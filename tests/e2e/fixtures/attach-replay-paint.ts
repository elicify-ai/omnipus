// Synthetic R2/R3 transport fixture. This is replay PAINTING coverage, NOT
// automatic sizing/geometry acceptance (T07/T16/T28 own that proof).
// Squad-lead Q1 A: control only the browser WebSocket boundary's attach input;
// the actual app replay buffer, decoder, store and renderer are unchanged.
import type { Page } from '@playwright/test'
import type { ClientFrame, ServerFrame } from '../../../src/lib/api/generated/asyncapi-types'
import type { Message } from '../../../src/lib/api/generated/openapi-types'
import { AttachSessionFrame as AttachSchema } from '../../../src/lib/api/generated/ws-schemas'
import { fixtureHistoryPosition, wireHistoryPage, wireHistorySnapshot } from '../../chat-history-paging/legacy-wire'

export async function installPaintAttachInput(page: Page, sessionId: string): Promise<void> {
  await page.addInitScript(({ targetSession }) => {
    const send = WebSocket.prototype.send
    WebSocket.prototype.send = function (this: WebSocket, data: Parameters<WebSocket['send']>[0]) {
      if (typeof data !== 'string' || !/\/api\/v1\/chat\/ws(?:\?|$)/.test(this.url)) return send.call(this, data)
      let frame: ClientFrame
      try { frame = JSON.parse(data) } catch (cause) {
        throw new Error('Replay-paint fixture received malformed client JSON', { cause })
      }
      if (frame.type !== 'attach_session' || frame.session_id !== targetSession) return send.call(this, data)
      const input = { ...frame, history_limit: 100, history_min_rows: 20 } satisfies Extract<ClientFrame, { type: 'attach_session' }>
      return send.call(this, JSON.stringify(input))
    }
  }, { targetSession: sessionId })
}

export function replayPaintFrames(rawAttach: unknown): ServerFrame[] {
  // This guard observes the ACTUAL client wire after the explicitly approved
  // boundary input, not an invented server limit100 for a captured request40.
  const request = AttachSchema.parse(rawAttach)
  if (request.history_limit !== 100 || request.history_min_rows !== 20) {
    throw new Error('Replay-paint fixture requires captured attach history_limit=100 and history_min_rows=20; not an automatic-sizing test')
  }
  const sessionId = request.session_id
  const entries: Message[] = []
  const carriers: ServerFrame[] = []
  const timestamp = '2026-10-22T08:00:00Z'
  for (let i = 0; i < 30; i++) {
    const questionId = `question-${i}`
    const answerId = `answer-${i}`
    const questionPosition = fixtureHistoryPosition(sessionId, questionId)
    const answerPosition = fixtureHistoryPosition(sessionId, answerId)
    const question = `Saved question ${i}`
    const answer = i === 29 ? 'REPLAY_LAST_ROW' : `Saved answer ${i}\n\n${'Historical paragraph. '.repeat(i % 3 === 0 ? 80 : 8)}`
    const params = { path: 'note.txt' }
    const result = { content: 'Saved result' }
    const questionOrigin = { position: questionPosition, entry_id: questionId }
    const answerOrigin = { position: answerPosition, entry_id: answerId }
    entries.push(
      { id: questionId, type: 'message', role: 'user', agent_id: 'mia', content: question, timestamp, history_position: questionPosition },
      { id: answerId, type: 'message', role: 'assistant', agent_id: 'mia', content: answer, timestamp, history_position: answerPosition,
        tool_calls: [{ id: `call-${i}`, tool: 'read_file', parameters: params, status: 'success', result, content_state: 'full' }] },
    )
    carriers.push(
      { type: 'replay_message', session_id: sessionId, id: questionId, role: 'user', content: question, history_origin: questionOrigin },
      { type: 'tool_call_start', session_id: sessionId, call_id: `call-${i}`, tool: 'read_file', params, history_origin: answerOrigin },
      { type: 'tool_call_result', session_id: sessionId, call_id: `call-${i}`, tool: 'read_file', status: 'success', result, history_origin: answerOrigin },
      { type: 'replay_message', session_id: sessionId, id: answerId, role: 'assistant', content: answer, history_origin: answerOrigin },
    )
  }
  // 30 questions +30 assistant owners =60 sources; narration60 +tool rows30
  // =90 logical rows. The120 carriers do not become120 source groups/rows.
  const history = wireHistoryPage(sessionId, entries, 90, {
    info: { limit: request.history_limit, min_rows: request.history_min_rows },
  })
  return [
    wireHistorySnapshot(history, { seq: 100, boot_id: 'replay-browser', reason: 'unknown_position' }, carriers),
    ...carriers,
    { type: 'done', session_id: sessionId, stats: { frames_emitted: carriers.length } },
  ]
}
