import { act } from 'react'
import { readFileSync } from 'node:fs'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from '@/store/chat/store'
import { getMessages, makeBucketMessages } from '@/store/chat/messages'
import { emptySessionState } from '@/store/chat/session'
import type { ChatMessage } from '@/store/chat/types'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { parseFrameSafe } from '@/lib/ws'
import {
  ClientFrameTypes,
  type ClientFrame,
  type ServerFrame,
  type WsFrame,
} from '@/lib/api/generated/asyncapi-types'

// not-wire-format: producer INPUT container, not a new cross-boundary type.
// Go's paired producer test records these actual gateway frames while the
// real provider is held in flight. Browser-state expectations below derive
// from chief item 1 (informational notice), never from the recorded output.
type ProducerInputs = {
  session_id: string
  turn_id: string
  message_id: string
  notice_index: number
  provider_was_held: boolean
  frames: unknown[]
}

const recorded = JSON.parse(readFileSync('tests/fixtures/attachments-1435-round3/media-unavailable-frames.json', 'utf8')) as ProducerInputs
let caseNumber = 0
let inputs: ProducerInputs
let send: ReturnType<typeof vi.fn>

function renameInputIdentity(value: unknown, identities: Map<string, string>): unknown {
  if (typeof value === 'string') return identities.get(value) ?? value
  if (Array.isArray(value)) return value.map((child) => renameInputIdentity(child, identities))
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, renameInputIdentity(child, identities)]))
  return value
}

beforeEach(() => {
  caseNumber += 1
  const identities = new Map([
    [recorded.session_id, `1435-r3-session-${caseNumber}`],
    [recorded.turn_id, `1435-r3-turn-${caseNumber}`],
    [recorded.message_id, `1435-r3-reply-${caseNumber}`],
  ])
  inputs = renameInputIdentity(recorded, identities) as ProducerInputs
  send = vi.fn().mockReturnValue(true) // WebSocket transport edge only; stores/reducers remain real.
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
  act(() => {
    useChatStore.setState({ sessionsById: {}, ...makeBucketMessages([]), isStreaming: false, outboundQueue: [], pendingDrainQueue: [] })
    useSessionStore.setState({ activeSessionId: inputs.session_id, activeAgentId: 'mia', activeAgentType: null, attachedSessionType: null })
    useConnectionStore.setState({
      connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
      isConnected: true, connectionError: null, liteMode: true,
    })
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

afterEach(() => {
  act(() => {
    useChatStore.setState({ outboundQueue: [], pendingDrainQueue: [] })
    useChatStore.getState().clearStreamingState()
    useSessionStore.setState({ activeSessionId: null })
    useConnectionStore.setState({ connection: null, isConnected: false })
  })
  vi.unstubAllGlobals()
})

// Type predicate over the generated ClientFrameTypes constant — the SAME
// generated source src/lib/ws.ts builds its CLIENT_FRAME_TYPES direction
// filter from. Claiming ClientFrame for a frame whose discriminator is in
// that list mirrors the generated unions exactly (ClientFrame's members are
// precisely the ClientFrameTypes discriminants).
function isClientDirection(frame: WsFrame): frame is ClientFrame {
  return (ClientFrameTypes as readonly string[]).includes(frame.type)
}

// Parse an inbound frame the way the real client path does before the store
// consumes it: full generated-schema validation (parseFrameSafe) plus the
// client→server direction filter that src/lib/ws.ts::_parseServerFrame
// applies on the live WebSocket path. A client-direction frame reaching
// handleFrame is a real defect, so the filter THROWS; the surviving type is
// ServerFrame by exhaustive subtraction — no cast, nothing masked.
function parseServerFrame(raw: unknown): ServerFrame {
  const frame = parseFrameSafe(raw)
  if (frame === null) throw new Error('instrument: frame failed the generated SPA wire schema')
  if (isClientDirection(frame)) {
    throw new TypeError(`instrument: client-direction frame "${frame.type}" must never reach handleFrame`)
  }
  return frame
}

function feed(raw: unknown): void {
  act(() => { useChatStore.getState().handleFrame(parseServerFrame(raw)) })
}
const bucket = () => useChatStore.getState().sessionsById[inputs.session_id]

function establishActualRunningState(): void {
  expect(inputs.provider_was_held, 'producer proof: backend work was still running after this notice').toBe(true)
  for (const frame of inputs.frames.slice(0, inputs.notice_index)) feed(frame)
  const placeholder: ChatMessage = {
    id: inputs.message_id, role: 'assistant', content: '',
    timestamp: '2026-10-26T00:00:00Z', status: 'streaming', isStreaming: true, agentId: 'mia',
  }
  act(() => {
    const state = { ...emptySessionState(), ...bucket(), ...makeBucketMessages([placeholder]), isStreaming: true }
    useChatStore.setState({ sessionsById: { [inputs.session_id]: state }, ...makeBucketMessages([placeholder]), isStreaming: true })
  })
  // A schema-valid running-turn announcement is an independent state INPUT
  // through the real reducer; it retains the preceding wire cursor. The
  // notice, subsequent tokens and done come from the actual gateway producer.
  feed({ type: 'session_state', session_id: inputs.session_id, user_id: 'tester',
    emitted_at: '2026-10-26T00:00:00Z', pending_approvals: [], pending_asks: [],
    active_turn: { turn_id: inputs.turn_id, agent_id: 'mia', started_at: '2026-10-26T00:00:00Z' } })
  expect(bucket().isStreaming).toBe(true)
  expect(bucket().activeTurnId).toBe(inputs.turn_id)
  expect(bucket().messagesById[inputs.message_id].status).toBe('streaming')
}

describe('#1435 Round 3 item 1 — actual informational media notice is nonterminal', () => {
  it('keeps the same running turn and reply until the actual gateway done frame', () => {
    establishActualRunningState()
    feed(inputs.frames[inputs.notice_index])
    expect.soft(bucket().isStreaming, 'item 1: informational notice must NOT end the running turn').toBe(true)
    expect.soft(useChatStore.getState().isStreaming, 'Stop availability must remain backed by the running turn').toBe(true)
    expect.soft(bucket().activeTurnId, 'informational notice must not clear active-turn identity').toBe(inputs.turn_id)
    expect.soft(bucket().messagesById[inputs.message_id].status).toBe('streaming')
    expect.soft(bucket().messagesById[inputs.message_id].content, 'notice must not replace the pending reply').toBe('')
    const notices = getMessages(bucket()).filter((message) => message.errorCode === 'media_unavailable')
    expect.soft(notices).toHaveLength(1)
    expect.soft(notices[0]?.id, 'the notice must be separate from the ongoing reply').not.toBe(inputs.message_id)
    let doneSeen = false
    for (const raw of inputs.frames.slice(inputs.notice_index + 1)) {
      const frame = parseServerFrame(raw)
      if (frame.type === 'done') {
        expect.soft(bucket().isStreaming, 'only the genuine done may finish this turn').toBe(true)
        doneSeen = true
      }
      act(() => { useChatStore.getState().handleFrame(frame) })
    }
    expect(doneSeen, 'instrument: actual producer completion frame was consumed').toBe(true)
    expect(bucket().isStreaming, 'genuine done control must finish the turn').toBe(false)
    expect(bucket().activeTurnId).toBeNull()
    expect.soft(bucket().messagesById[inputs.message_id].content,
      'tokens after the notice must still reach the same reply').toBe('Working on the remaining request. Still progressing.')
  })

  it('does not drain queued input before the real done; the real Stop action stays usable', () => {
    establishActualRunningState()
    act(() => { useChatStore.setState({ pendingDrainQueue: ['Wait for the real completion.'] }) })
    feed(inputs.frames[inputs.notice_index])
    expect.soft(useChatStore.getState().pendingDrainQueue,
      'informational notice must not call the terminal queued-message drain').toEqual(['Wait for the real completion.'])
    expect.soft(send.mock.calls, 'no next user request may be sent while the provider is held').toHaveLength(0)
    expect.soft(bucket().isStreaming).toBe(true)
    act(() => { useChatStore.getState().cancelStream() })
    expect(send.mock.calls.map(([frame]) => frame), 'actual Stop action reaches the WebSocket edge')
      .toContainEqual({ type: 'cancel', session_id: inputs.session_id })
  })
})
