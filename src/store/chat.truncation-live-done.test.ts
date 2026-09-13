// chat.truncation-live-done.test.ts — ADR-087 D2, finding #10.
//
// Review finding #10: `truncated`/`truncation_reason` reached the SPA ONLY
// via ReplayMessageFrame (WS replay) and the REST `Message` (cold-load) — a
// turn that hit the output limit WHILE THE USER WAS WATCHING got no
// "(cut off at the output limit)" suffix until a reload or reconnect
// replayed it back in. The live `done` frame's `stats` object had no
// truncation fields at all.
//
// The fix adds `DoneStats.truncated`/`DoneStats.truncation_reason` (mirrors
// `Message.truncation_reason`, contracts/components/schemas/DoneStats.yaml)
// and stamps them onto the finishing bubble in the `case 'done'` reducer
// (src/store/chat.ts), using the SAME `normalizeTruncationReason` legacy-
// default helper the replay path already uses — see
// `chat.truncation-replay.test.ts` for the replay-side sibling coverage this
// file mirrors on the live path.
//
// Harness pattern (handleFrame + act, seeded bucket with a live streaming
// placeholder) mirrored from chat.error-terminal.test.ts, which is the only
// other suite driving a live terminal `done` through the real reducer.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import { getMessageStatusSuffix, INTERRUPTED_SUFFIX_TEXT, CUT_OFF_SUFFIX_TEXT } from '@/lib/truncation'

const SID = 'truncation-live-done-test'

function resetStore(): void {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia', activeAgentType: null })
  })
}

beforeEach(resetStore)

/** The optimistic streaming placeholder a live turn is finalized on top of. */
function streamingPlaceholder(content: string): ChatMessage {
  return {
    id: 'live-assistant-bubble',
    role: 'assistant',
    content,
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    agentId: 'mia',
  } as ChatMessage
}

/** Seed sessionsById[SID] (required — an unseeded sid hits the unknown-sid
 * branch of `case 'done'` and never reaches the finalize/stamp logic). */
function seedBucket(seedMessages: ChatMessage[]): void {
  act(() => {
    useChatStore.setState(() => ({
      sessionsById: {
        [SID]: {
          ...makeBucketMessages(seedMessages),
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          isStreaming: seedMessages.some((m) => m.isStreaming),
          isReplaying: false,
          replayCompletedForSession: null,
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          cancelStage: null,
          lastUserMessageAt: null,
          lastReceivedEventTime: null,
          spanByParentCallId: {},
        },
      },
      messages: seedMessages,
      toolCalls: {},
      toolCallOrder: [],
      isStreaming: seedMessages.some((m) => m.isStreaming),
      isReplaying: false,
    }))
  })
}

function lastAssistantMessage(): ChatMessage {
  const b = useChatStore.getState().sessionsById[SID]
  const msgs = b ? getMessages(b) : []
  const m = [...msgs].reverse().find((x) => x.role === 'assistant')
  expect(m).toBeDefined()
  return m!
}

describe('chat store — live `done` frame truncation plumbing (ADR-087 D2, finding #10)', () => {
  it('a done frame with stats.truncated + max_output_tokens marks the last assistant message, live, with no reload/replay', () => {
    seedBucket([streamingPlaceholder('Here is the start of a long answer that gets cut off before')])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 512, cost: 0.002, duration_ms: 900, truncated: true, truncation_reason: 'max_output_tokens' },
      })
    })

    const msg = lastAssistantMessage()
    expect(msg.truncated).toBe(true)
    expect(msg.truncationReason).toBe('max_output_tokens')
    expect(msg.isStreaming).toBe(false)
    expect(msg.status).toBe('done')
    // This IS the render-layer contract every AssistantMessage/InterruptedMessageMarkers
    // site consumes (src/lib/truncation.ts) — proves the live bubble renders the
    // suffix the instant this reducer runs, no reload/replay required.
    expect(getMessageStatusSuffix(msg)).toBe(CUT_OFF_SUFFIX_TEXT)
  })

  it('a done frame with stats.truncated + cancelled renders "(interrupted)" only (D1 precedence)', () => {
    seedBucket([streamingPlaceholder('Working on it and then')])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 40, cost: 0.0001, truncated: true, truncation_reason: 'cancelled' },
      })
    })

    const msg = lastAssistantMessage()
    expect(msg.truncated).toBe(true)
    expect(msg.truncationReason).toBe('cancelled')
    expect(getMessageStatusSuffix(msg)).toBe(INTERRUPTED_SUFFIX_TEXT)
    expect(getMessageStatusSuffix(msg)).not.toBe(CUT_OFF_SUFFIX_TEXT)
  })

  it('D1 precedence, dual signal live case: a bubble already status "interrupted" (e.g. an earlier cancel frame) that then receives a done frame carrying stats.truncated + max_output_tokens still renders "(interrupted)" only — never both', () => {
    // Simulates the ordering where a cancel-driven status:'interrupted' lands
    // on the bubble before the turn's own `done` frame (which still carries
    // whatever the provider's finish_reason was at the moment it stopped).
    const placeholder = streamingPlaceholder('Working on it and then')
    placeholder.status = 'interrupted'
    seedBucket([placeholder])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 20, cost: 0.0001, truncated: true, truncation_reason: 'max_output_tokens' },
      })
    })

    const msg = lastAssistantMessage()
    // The finalize sweep never demotes 'interrupted' (FR-21) — it survives
    // the done frame regardless of what stats.truncation_reason says.
    expect(msg.status).toBe('interrupted')
    expect(msg.truncated).toBe(true)
    expect(msg.truncationReason).toBe('max_output_tokens')
    // The precedence function is what resolves the conflict: status wins.
    expect(getMessageStatusSuffix(msg)).toBe(INTERRUPTED_SUFFIX_TEXT)
    expect(getMessageStatusSuffix(msg)).not.toBe(CUT_OFF_SUFFIX_TEXT)
  })

  it('a plain done frame (no truncated stats) changes nothing on the finishing bubble — mutation target', () => {
    seedBucket([streamingPlaceholder('A complete answer, no cutoff.')])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 30, cost: 0.0001 },
      })
    })

    const msg = lastAssistantMessage()
    expect(msg.truncated).toBeUndefined()
    expect(msg.truncationReason).toBeUndefined()
    expect(getMessageStatusSuffix(msg)).toBeNull()
  })

  it('a done frame with no stats object at all leaves truncated/truncationReason unset', () => {
    seedBucket([streamingPlaceholder('Outbound-fallback done, no stats.')])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
      })
    })

    const msg = lastAssistantMessage()
    expect(msg.truncated).toBeUndefined()
    expect(msg.truncationReason).toBeUndefined()
  })
})
