// Issue #1428 — cross-chat draft leak (chief addition, navigation hands-on
// finding on the #1266 candidate 7d2d03675; UAT evidence row P07: the draft
// "P06 draft — do not send." typed in JIM's session appeared in EXTRA CHAT's
// composer — "the composer draft is not scoped per chat").
//
// Oracle (chief control entry, dispatch 1428-red-r1): a draft belongs only to
// the conversation it was typed in, survives switching away and back, and
// never appears in another chat's composer.
//
// This file is deliberately dependency-minimal (stores + real runtime only —
// no slash hooks, no frame-reset helpers) so the IDENTICAL file runs on the
// release tree for the PRE-EXISTING/NEW comparison (release e83647cc1).

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import type { WsConnection } from '@/lib/ws'
import type { components } from '@/lib/api/generated/openapi-types'

// Jim's main chat — where the draft is typed (the P07 shape).
const SID_MAIN = 'session_01JCEDRAFTMAINJIMCHAT00001'
// The unrelated extra chat (Mia's) the draft must never leak into.
const SID_EXTRA = 'session_01JCEDRAFTEXTRAMIACHAT00001'
const DRAFT_MAIN = 'P06 draft — do not send.'
const DRAFT_EXTRA = 'Extra chat own draft.'

function seedKnownSession(sessionId: string, agentId: string): void {
  const session: Pick<components['schemas']['Session'], 'id' | 'agent_id' | 'workspace_id'> = {
    id: sessionId,
    agent_id: agentId,
    workspace_id: '01JCEDRAFTWORKSPACE0000000000',
  }
  queryClient.setQueryData(['session-detail', sessionId], { session })
}

beforeEach(() => {
  queryClient.clear()
  localStorage.clear()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    seedKnownSession(SID_MAIN, 'jim')
    seedKnownSession(SID_EXTRA, 'mia')
    useSessionStore.getState().setActiveSession(SID_MAIN, 'jim')
    useConnectionStore.setState({
      connection: { send: vi.fn().mockReturnValue(true), isConnected: true, connect: vi.fn(), disconnect: vi.fn() } as unknown as WsConnection,
      isConnected: true,
    })
  })
})

afterEach(() => queryClient.clear())

describe('issue #1428 — the composer draft is scoped to its own conversation', () => {
  it('a draft typed in one chat never appears in another chat and survives switching away and back', () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    // Draft typed in the main chat.
    act(() => result.current.thread.composer.setText(DRAFT_MAIN))
    expect(result.current.thread.composer.getState().text).toBe(DRAFT_MAIN)

    // Switch to the extra chat: its composer must NOT carry the main chat's
    // draft — the extra chat has no draft of its own, so it shows none.
    act(() => {
      expect(useSessionStore.getState().attachToSession(SID_EXTRA, 'chat', 'Extra chat', 'mia')).toBe(true)
    })
    expect(result.current.thread.composer.getState().text,
      'chief control: the main-chat draft must never appear in another chat composer').toBe('')

    // The extra chat gets its OWN draft.
    act(() => result.current.thread.composer.setText(DRAFT_EXTRA))
    expect(result.current.thread.composer.getState().text).toBe(DRAFT_EXTRA)

    // Back to the main chat: its draft survived the round trip…
    act(() => {
      expect(useSessionStore.getState().attachToSession(SID_MAIN, 'chat', 'Main chat', 'jim')).toBe(true)
    })
    expect(result.current.thread.composer.getState().text,
      'chief control: the draft survives switching away and back').toBe(DRAFT_MAIN)

    // …and the extra chat still holds its own, not the main chat's.
    act(() => {
      expect(useSessionStore.getState().attachToSession(SID_EXTRA, 'chat', 'Extra chat', 'mia')).toBe(true)
    })
    expect(result.current.thread.composer.getState().text).toBe(DRAFT_EXTRA)
  })
})
