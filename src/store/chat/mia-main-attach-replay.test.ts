// Founder bug (2026-10-11, nav-miastuck lane): opening Mia's MAIN chat showed
// "Waiting for response…" with a Stop button although the chat is idle — no
// turn running. The composer placeholder and the Stop control are driven by
// the store's `isStreaming` flag (ChatScreen::composerPlaceholder,
// useCancelState), so the invariant under test is:
//
//   Attaching to an idle chat — a snapshot rebuild of a transcript whose every
//   turn is complete, with a session_state that announces NO active turn —
//   must end with isStreaming === false and no streaming message and no
//   running tool.
//
// The frame sequence below is not hand-derived: it is the EXACT sequence the
// real gateway replay path emitted for the recorded session copy
// (mia-main-attach-frames.fixture.ts, captured frame-by-frame). The capture
// showed the attach's session_state carrying NO active_turn, so if this test
// is red the busy state was armed by the SPA's own handling of the replay,
// not by a server announcement.

import { beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import type { ServerFrame } from '@/lib/ws'
import { MIA_MAIN_ATTACH_FRAMES } from './mia-main-attach-frames.fixture'

const SID = 'main-session-01M4MT790K750PBFPY4VXFC6RF+mia'

const bucket = () => useChatStore.getState()

function feed(...frames: Array<Record<string, unknown>>): void {
  act(() => {
    for (const fr of frames) {
      useChatStore.getState().handleFrame({ session_id: SID, ...fr } as unknown as ServerFrame)
    }
  })
}

beforeEach(() => {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({ sessionsById: {}, messages: [], isStreaming: false } as never)
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia', activeAgentType: null })
    // The real attach flow (OmnipusRuntimeProvider.tsx / attachToSession) marks
    // the bucket replaying right after sending attach_session and BEFORE any
    // frame arrives — the replayed tool_call_start frames must be treated as
    // history (shouldMarkStreaming = !isReplaying), exactly as in the live flow.
    useChatStore.getState().setReplaying(true)
  })
})

describe('attaching to the recorded idle Mia main chat', () => {
  it('ends idle: no busy flag, no streaming bubble, no running tool', () => {
    expect(bucket().isStreaming, 'the store starts idle').toBe(false)

    feed(...MIA_MAIN_ATTACH_FRAMES)

    expect(bucket().isStreaming, 'an idle replayed chat must not show Stop / "Waiting for response..."').toBe(false)
    expect(bucket().messages.some((m) => m.isStreaming || m.status === 'streaming'),
      'no replayed bubble may stay open').toBe(false)
    expect(Object.values(bucket().toolCalls).some((t) => t.status === 'running'),
      'no replayed tool call may stay running').toBe(false)
    const last = bucket().messages[bucket().messages.length - 1]
    expect(last?.role).toBe('assistant')
    expect(last?.status).toBe('done')
    expect(useChatStore.getState().sessionsById[SID]?.activeTurnId,
      'no turn may stay announced active').toBeFalsy()
  })

  it('the same rebuild DOES go busy when the server announces an active turn (instrument check)', () => {
    // Proves the fixture can produce a busy state at all: the only difference
    // from the idle case is the server's active_turn announcement, which the
    // real capture did NOT contain.
    const withActiveTurn = MIA_MAIN_ATTACH_FRAMES.map((f) =>
      f.type === 'session_state' && f.session_id ? { ...f, active_turn: { turn_id: 'mia-turn-8', agent_id: 'mia', started_at: '2026-10-11T11:10:51Z' } } : f,
    )
    feed(...withActiveTurn)
    expect(bucket().isStreaming).toBe(true)
  })
})
