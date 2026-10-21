// REAL full application route tree, ChatScreen, AssistantUI runtime, generated
// adapter and stores. HTTP/WebSocket are the ONLY application doubles.
import { act, screen, waitFor } from '@testing-library/react'
import { expect, vi } from 'vitest'
import type { Message, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { queryClient } from '@/lib/queryClient'
import { MAIN_ID, json, TestSocket } from '../l1-room-ui/harness'
import {
  mountStage1App, resetStage1App, settledMainChat, Stage1Network, stopStage1App,
} from '../l1-room-ui/stage1-harness'
import { deferred, expectPageAccounting, expectValidFrame, page, replay, snapshot, STAMP } from './fixtures'

// Read from the real ChatScreen::WelcomeState heading, not a nonexistent test id.
// The valid-empty control below the UI tests must keep this negative oracle live.
export const WELCOME_HEADING = { level: 1, name: /^Welcome to\b/ } as const

const originalClientHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight')
const originalRect = HTMLElement.prototype.getBoundingClientRect

export async function openHistory(initial: SessionHistoryResponse, options: { plain?: boolean; path?: string } = {}) {
  resetStage1App()
  if (options.plain !== false) {
    vi.stubGlobal('ResizeObserver', undefined)
    // D5 plain/manual REQUEST fixture only: supply an 800px browser viewport
    // at the DOM edge, A starts at80. This is NOT measured virtualizer/pixel
    // or anchor-drift evidence; T07/T10 real layout belongs to browser E2E.
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get() {
      return this.getAttribute('aria-label') === 'Conversation history' ? 800 : originalClientHeight?.get?.call(this) ?? 0
    } })
    HTMLElement.prototype.getBoundingClientRect = function () {
      return this.getAttribute('aria-label') === 'Conversation history'
        ? DOMRect.fromRect({ width: 1024, height: 800 }) : originalRect.call(this)
    }
  }
  const network = new Stage1Network()
  const initialRest = deferred<Response>()
  // Hold the legacy competing REST open. WS is authoritative (D6); allowing
  // this unrelated adapter failure to hide the shell would mask control tests.
  // The request is not treated as valid behavior or silently answered with [].
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => initialRest.promise)
  const mounted = await mountStage1App(options.path)
  if (!options.path) await settledMainChat(mounted.socket)
  const sessionId = useSessionStore.getState().activeSessionId
  if (sessionId !== MAIN_ID) throw new Error(`Fixture instrument: expected main session ${MAIN_ID}, got ${sessionId}`)
  await sendSnapshot(mounted.socket, { ...initial, session_id: MAIN_ID })
  if (initial.entries.length > 0) {
    await screen.findByText(initial.entries.at(-1)!.content!, { exact: true })
  }
  return { ...mounted, network, initialRest }
}

export async function sendFrames(socket: TestSocket, incoming: ServerFrame[]) {
  incoming.forEach(expectValidFrame)
  await act(async () => incoming.forEach((frame) => socket.receive(frame)))
}

export async function sendSnapshot(socket: TestSocket, history: SessionHistoryResponse, reason: Parameters<typeof snapshot>[1] = 'unknown_position', seq = 100) {
  expectPageAccounting(history, history.entries.map((entry) => entry.history_position!), history.entries.length)
  await sendFrames(socket, [
    { ...snapshot(history, reason), seq },
    { type: 'session_state', session_id: history.session_id, user_id: 'history-reader', history_state: { status: 'available' }, pending_approvals: [], emitted_at: STAMP },
    ...history.entries.map((source) => replay(source, history.session_id)),
    { type: 'done', session_id: history.session_id },
    { type: 'catch_up_complete', session_id: history.session_id, boot_id: 'history-boot', seq, mode: 'snapshot' },
  ])
  await waitFor(() => expect(useChatStore.getState().sessionsById[history.session_id]?.cursor).toStrictEqual({ bootId: 'history-boot', seq }))
}

export function historyRequests(network: Stage1Network) {
  return network.requests.filter((request) => request.path === `/api/v1/sessions/${MAIN_ID}/messages`)
}
export function sentMessages(socket: TestSocket) {
  return socket.sent.filter((frame) => frame.type === 'message')
}
export function wirePage(entries: Message[], earlier = false, mode: SessionHistoryResponse['page_info']['mode'] = 'older') {
  return page(entries, { sessionId: MAIN_ID, earlier, later: mode === 'older' && entries.length > 0, info: { mode, limit: mode === 'latest' ? 40 : 20, min_rows: 20 } })
}
export async function stopHistory(app: Awaited<ReturnType<typeof openHistory>> | undefined) {
  if (app) app.initialRest.resolve(json(page([], { sessionId: MAIN_ID })))
  await stopStage1App()
  if (originalClientHeight) Object.defineProperty(HTMLElement.prototype, 'clientHeight', originalClientHeight)
  else Reflect.deleteProperty(HTMLElement.prototype, 'clientHeight')
  HTMLElement.prototype.getBoundingClientRect = originalRect
  queryClient.clear()
  if (app) expect(app.network.unexpected, 'all application HTTP is captured at declared process edges').toStrictEqual([])
}
