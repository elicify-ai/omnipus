// #1428 post-fix coverage, authored AFTER implementation 5fec8eb6d.
// This is NOT pre-fix RED and NOT the author's own CHECK/mutation proof.
//
// Oracle: chief Q1 A, 2026-10-25 — this tab's live main attach wins over a
// disagreeing shared descriptor; repeated same-session re-seeding preserves
// main/title in the persisted workspace descriptor. Shapes identified by the
// independent CHECK's held-out probes A/B in omnipus-scratch/1428-check.md.
//
// REAL: FeedKindLabel, query cache, stores, persistence and reattach helper.
// DOUBLE: outbound network sender only; roster data is a real query-cache
// fixture. No component/store/runtime/API function is mocked or spied on.
// Cases: adversarial downgraded descriptor (M2 shape); two same-session
// reconnect re-seeds with exact in-memory AND persisted descriptors (M3).
// Gaps: no new browser/reload/provider campaign; original tests stay intact.
// Independent CHECK owns the M2/M3 mutant proof and its verdict.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { FeedKindLabel } from '@/components/chat/FeedKindLabel'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type { ReattachSender } from '@/components/chat/OmnipusRuntimeProvider'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { readPersistedSessionByWorkspace, useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'

const SID = 'session_01JCE1428HEADERCOVERAGE0001'
const WORKSPACE = 'workspace_01JCE1428HEADERCOVERAGE01'
const OWNER = 'jim'
const TITLE = 'Jim - Main Session' // Chief's expected main-chat label, not implementation output.
const MAIN_DESCRIPTOR = { id: SID, type: 'main' as const, title: TITLE, agentId: OWNER }

beforeEach(() => {
  queryClient.clear()
  localStorage.clear()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    useConnectionStore.setState({
      connection: { send: vi.fn().mockReturnValue(true), isConnected: true } as unknown as WsConnection,
      isConnected: true,
    })
  })
  queryClient.setQueryData(['agents'], [makeAgent({ id: OWNER, name: 'Jim', type: 'core', locked: true })])
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  localStorage.clear()
})

function attachMain() {
  act(() => {
    expect(useSessionStore.getState().attachToSession(SID, 'main', TITLE, OWNER), 'fixture attaches the real main session').toBe(true)
  })
}

describe('#1428 post-fix header coverage — independent mutant proof deferred to CHECK', () => {
  it('M2 shape: labels the live Jim main even when its shared workspace descriptor is downgraded to chat', () => {
    attachMain()
    render(<QueryClientProvider client={queryClient}><FeedKindLabel /></QueryClientProvider>)
    expect(screen.getByRole('status', { name: TITLE, exact: true }), 'instrument sees the correct main label before disagreement').toBeVisible()
    expect(screen.getByTestId('feed-kind-label').textContent).toBe(TITLE)

    const downgraded = { id: SID, type: 'chat' as const, title: null, agentId: OWNER }
    act(() => useSessionStore.getState().setWorkspaceSessionDescriptor(WORKSPACE, downgraded))
    const state = useSessionStore.getState()
    expect(state.sessionByWorkspace[WORKSPACE], 'negative fixture really disagrees with the live main kind').toEqual(downgraded)
    expect({ id: state.activeSessionId, owner: state.activeAgentId, type: state.attachedSessionType }).toEqual({
      id: SID, owner: OWNER, type: 'main',
    })
    expect(screen.getByTestId('feed-kind-label').textContent,
      'chief Q1 A: this tab live attach, not the downgraded shared snapshot, names the header').toBe(TITLE)
    expect(screen.getByTestId('feed-kind-label')).toHaveAccessibleName(TITLE)
  })

  it('M3 shape: repeated reconnect re-seeds keep main/title in both the workspace descriptor and persistent storage', () => {
    attachMain()
    expect(useSessionStore.getState().sessionByWorkspace[WORKSPACE], 'instrument starts with a main descriptor').toEqual(MAIN_DESCRIPTOR)
    expect(readPersistedSessionByWorkspace(), 'instrument reads the actual stored initial descriptor').toEqual({ [WORKSPACE]: MAIN_DESCRIPTOR })

    // A fresh transport on each reconnect prevents attach-in-flight coalescing
    // from turning this into a no-op; both real re-seed paths must execute.
    for (const phase of ['first reconnect', 'repeated reconnect']) {
      const transport = { send: vi.fn().mockReturnValue(true) } satisfies ReattachSender
      act(() => {
        useConnectionStore.getState().setConnection(transport as unknown as WsConnection)
        expect(reattachActiveSession(transport, useConnectionStore.getState().setConnectionError), phase).toBe(true)
      })
      expect(transport.send.mock.calls, `${phase}: the real helper sends a same-session attach`).toEqual([
        [{ type: 'attach_session', session_id: SID }],
      ])
      const state = useSessionStore.getState()
      expect({
        id: state.activeSessionId,
        owner: state.activeAgentId,
        type: state.attachedSessionType,
        title: state.attachedTaskTitle,
        descriptor: state.sessionByWorkspace[WORKSPACE],
        persisted: readPersistedSessionByWorkspace(),
      }, `${phase}: re-seeding must not persist an ordinary chat or lose the main title`).toEqual({
        id: SID,
        owner: OWNER,
        type: 'main',
        title: TITLE,
        descriptor: MAIN_DESCRIPTOR,
        persisted: { [WORKSPACE]: MAIN_DESCRIPTOR },
      })
      expect(useConnectionStore.getState().connectionError).toBeNull()
    }
  })
})
