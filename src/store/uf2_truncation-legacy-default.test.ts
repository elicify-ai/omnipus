// RED pack — WC-FIX RED-F, unit UF2 (DEL-F36).
//
// Spec source: docs/internal/specs/session-core-spec.md — DEL-F36 and BDD-12.2.
//
//   DEL-F36  src/lib/truncation.ts::normalizeTruncationReason; consumers
//            rawToMessage / handleReplayMessageFrame / createFrameSlice (`done`)
//            — old branch: explicit legacy default `reason ?? 'cancelled'` when
//            truncated. Replacement: "Explicit truncation_reason; no default
//            cancelled for unexplained old truncated entry."
//
// Oracle provenance: the spec's replacement column. A `done` frame that marks
// the turn truncated (`stats.truncated === true`) but carries NO explicit
// `stats.truncation_reason` must NOT be rendered as a user cancel — the legacy
// "absent reason means cancelled" default is removed. The observable
// consequence is that no "(interrupted)" status suffix is shown for that
// truncated turn (getMessageStatusSuffix keys 'cancelled' → INTERRUPTED_SUFFIX_TEXT).
//
// This is a store-level (behavioural) test on the live `done` carrier, chosen
// over a direct unit test on `normalizeTruncationReason` because DEL-F36's
// replacement may inline the helper; the observable contract — no invented
// 'cancelled' — is the same either way.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'
import { getMessageStatusSuffix, INTERRUPTED_SUFFIX_TEXT } from '@/lib/truncation'

const SESSION_ID = 'uf2-truncation-legacy-test'

function resetStore() {
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
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId: SESSION_ID, activeAgentId: 'jim', activeAgentType: null })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

describe('UF2/DEL-F36 — a truncated turn with no explicit reason is not defaulted to "cancelled"', () => {
  it('does not stamp "cancelled" on a done frame whose stats carry truncated:true and no truncation_reason', () => {
    // Open a streaming assistant bubble, exactly as a live turn does.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'partial answer', agent_id: 'jim', session_id: SESSION_ID,
      })
    })

    // The turn finishes truncated with NO explicit reason — the legacy default
    // (reason ?? 'cancelled') is what DEL-F36 removes.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SESSION_ID,
        stats: { truncated: true, tokens: 3, cost: 0.0001 },
      })
    })

    const assistant = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(assistant, 'the assistant bubble from the truncated turn must exist').toBeDefined()
    // Core assertion: no invented 'cancelled' reason.
    expect(assistant!.truncationReason).not.toBe('cancelled')
    // Observable consequence: no interrupted suffix is fabricated for it.
    expect(
      getMessageStatusSuffix({ role: 'assistant', status: assistant!.status, truncationReason: assistant!.truncationReason }),
    ).not.toBe(INTERRUPTED_SUFFIX_TEXT)
  })

  it('still honours an explicit truncation_reason on the done frame (canonical positive control)', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'partial answer', agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SESSION_ID,
        stats: { truncated: true, truncation_reason: 'max_output_tokens', tokens: 3, cost: 0.0001 },
      })
    })

    const assistant = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(assistant!.truncationReason).toBe('max_output_tokens')
  })
})
