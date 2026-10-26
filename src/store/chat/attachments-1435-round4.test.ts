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
import { codeToMessage } from '@/lib/llm-error'
import { ClientFrameTypes, type ClientFrame, type ServerFrame, type WsFrame } from '@/lib/api/generated/asyncapi-types'

// not-wire-format: producer INPUT container recorded by the paired Go test
// from the REAL gateway replay window (real loop, real store, real attach).
// Browser-state expectations below derive from chief round-4 item 1, never
// from the recorded output.
type ReplayInputs = {
  session_id: string
  turn_id: string
  replay_frames: unknown[]
  live_after_frames: unknown[]
}

const recorded = JSON.parse(readFileSync('tests/fixtures/attachments-1435-round4/replay-frames.json', 'utf8')) as ReplayInputs
let caseNumber = 0
let inputs: ReplayInputs
let replyId: string

function renameIdentity(value: unknown, identities: Map<string, string>): unknown {
  if (typeof value === 'string') return identities.get(value) ?? value
  if (Array.isArray(value)) return value.map((child) => renameIdentity(child, identities))
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, renameIdentity(child, identities)]))
  return value
}

// Same direction-filtered parse the round-3 test uses: full generated-schema
// validation plus the generated client→server filter; a client-direction
// frame throws instead of being masked.
function isClientDirection(frame: WsFrame): frame is ClientFrame {
  return (ClientFrameTypes as readonly string[]).includes(frame.type)
}
function parseServerFrame(raw: unknown): ServerFrame {
  const frame = parseFrameSafe(raw)
  if (frame === null) throw new Error('instrument: frame failed the generated SPA wire schema')
  if (isClientDirection(frame)) {
    throw new TypeError(`instrument: client-direction frame "${frame.type}" must never reach handleFrame`)
  }
  return frame
}

let send: ReturnType<typeof vi.fn>

beforeEach(() => {
  caseNumber += 1
  replyId = `1435-r4-reply-${caseNumber}`
  // The gateway-minted reply id from the real live continuation: the SPA
  // placeholder must adopt that exact identity so the continued tokens
  // land on the same reply.
  const rawReplyId = (inputs?.live_after_frames ?? recorded.live_after_frames)
    .map((frame) => frame as { type?: string; message_id?: string })
    .filter((frame) => frame.type === 'token' && typeof frame.message_id === 'string')
    .map((frame) => frame.message_id as string)[0]
  const identities = new Map([
    [recorded.session_id, `1435-r4-session-${caseNumber}`],
    [recorded.turn_id, `1435-r4-turn-${caseNumber}`],
  ])
  if (rawReplyId) identities.set(rawReplyId, replyId)
  inputs = renameIdentity(recorded, identities) as ReplayInputs
  send = vi.fn().mockReturnValue(true)
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

function feed(raw: unknown): void {
  act(() => { useChatStore.getState().handleFrame(parseServerFrame(raw)) })
}
const bucket = () => useChatStore.getState().sessionsById[inputs.session_id]
const noticeIndex = () => inputs.replay_frames.findIndex((frame) => (frame as { type?: string }).type === 'replay_error')
const catalogueWording = codeToMessage('media_unavailable')

describe('#1435 Round 4 item 1a — replayed notice is its own entry', () => {
  it('leaves the earlier empty truncated assistant entry untouched on reopen', () => {
    act(() => { useChatStore.getState().resetSessionForReplay(inputs.session_id) })
    for (const raw of inputs.replay_frames.slice(0, noticeIndex())) feed(raw)

    const earlier = getMessages(bucket()).find((m) => m.role === 'assistant' && m.content === '')
    expect(earlier, 'instrument: the real replay window created the earlier truncated assistant entry').toBeDefined()
    const before = { content: earlier!.content, status: earlier!.status, isStreaming: earlier!.isStreaming, errorCode: earlier!.errorCode }

    feed(inputs.replay_frames[noticeIndex()])

    const after = getMessages(bucket()).find((m) => m.id === earlier!.id)
    expect.soft(after?.content, '#1435 R4 1a: the earlier entry keeps its own content').toBe(before.content)
    expect.soft(after?.status, '#1435 R4 1a: the earlier entry keeps its own status').toBe(before.status)
    expect.soft(after?.errorCode, '#1435 R4 1a: the earlier entry must not be restamped as the error').toBeUndefined()
    const notices = getMessages(bucket()).filter((m) => m.errorCode === 'media_unavailable')
    expect.soft(notices, '#1435 R4 1a: the notice must be a separate entry').toHaveLength(1)
    expect.soft(notices[0]?.id, '#1435 R4 1a: separate identity from the earlier entry').not.toBe(earlier!.id)
    expect.soft(notices[0]?.content, '#1435 R4 1a: catalogue wording unchanged').toBe(catalogueWording)

    feed(inputs.replay_frames[noticeIndex() + 1]) // catch_up_complete closes the replay window
    expect(getMessages(bucket()).filter((m) => m.errorCode === 'media_unavailable')).toHaveLength(1)
  })
})

describe('#1435 Round 4 item 1b — replayed notice never touches a running reply', () => {
  it('keeps the streaming reply intact; later tokens reach the same reply until the real done', () => {
    const placeholder: ChatMessage = {
      id: replyId, role: 'assistant', content: '',
      timestamp: '2026-10-27T00:00:00Z', status: 'streaming', isStreaming: true, agentId: 'mia',
    }
    act(() => {
      const state = { ...emptySessionState(), ...makeBucketMessages([placeholder]), isStreaming: true }
      useChatStore.setState({ sessionsById: { [inputs.session_id]: state }, ...makeBucketMessages([placeholder]), isStreaming: true })
    })
    feed({ type: 'session_state', session_id: inputs.session_id, user_id: 'tester',
      emitted_at: '2026-10-27T00:00:00Z', pending_approvals: [], pending_asks: [],
      active_turn: { turn_id: inputs.turn_id, agent_id: 'mia', started_at: '2026-10-27T00:00:00Z' } })
    expect(bucket().isStreaming).toBe(true)

    feed(inputs.replay_frames[noticeIndex()])

    expect.soft(bucket().isStreaming, '#1435 R4 1b: the replayed notice must not end the running turn').toBe(true)
    expect.soft(bucket().activeTurnId, '#1435 R4 1b: active-turn identity must survive the replayed notice').toBe(inputs.turn_id)
    expect.soft(bucket().messagesById[replyId].status, '#1435 R4 1b: the reply must stay streaming').toBe('streaming')
    expect.soft(bucket().messagesById[replyId].content, '#1435 R4 1b: the reply must not be overwritten').toBe('')
    const notices = getMessages(bucket()).filter((m) => m.errorCode === 'media_unavailable')
    expect.soft(notices, '#1435 R4 1b: the notice is its own entry').toHaveLength(1)
    expect.soft(notices[0]?.content).toBe(catalogueWording)

    // The real live continuation recorded by the paired producer after the
    // provider resumed: the same tokens and the genuine done, seq-ascending
    // above the replay window.
    let lastTokenContent: string | undefined
    for (const raw of inputs.live_after_frames) {
      const frame = parseServerFrame(raw)
      if (frame.type === 'token') {
        expect.soft(bucket().isStreaming, '#1435 R4 1b: only the genuine done may finish this turn').toBe(true)
        lastTokenContent = (frame as { content?: string }).content
      }
      feed(raw)
    }
    expect.soft(bucket().messagesById[replyId].content,
      '#1435 R4 1b: tokens after the notice must still reach the same reply').toBe(lastTokenContent)
    expect(bucket().isStreaming, 'only the genuine done ends the turn').toBe(false)
  })
})
