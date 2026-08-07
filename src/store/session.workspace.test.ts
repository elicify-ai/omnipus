// Unit tests for the workspace-session memory additions to session.ts:
//   - sessionByWorkspace: Record<string, WorkspaceSessionDescriptor | null>
//   - enterWorkspaceChat(workspaceId): restores or freshes per workspace
//   - resolveWorkspaceSessionFromServer (D4 fix): server-derived restore when
//     sessionByWorkspace has no local descriptor for a workspace — covers the
//     cold-reload case where the in-memory pointer was wiped but the server's
//     sessions are still intact.
//
// All tests verify STATE OUTCOMES (not spy call counts) to avoid Zustand
// singleton spy-accumulation issues across tests.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useWorkspacesStore } from './workspacesStore'
import { useConnectionStore } from './connection'
import { useUiStore } from './ui'
import type { Session } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSessions: vi.fn(),
  }
})

import { fetchSessions } from '@/lib/api'
import { useSessionStore, registerChatResetForReplay } from './session'

// ── Helpers ───────────────────────────────────────────────────────────────────

function resetAll() {
  // Merge-only reset (no replace=true) to preserve Zustand action references.
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
  })
  useWorkspacesStore.setState({
    activeWorkspaceId: null,
    activePlanId: null,
    boardAltitude: 'top-level',
  })
  useConnectionStore.setState({
    connection: null,
    isConnected: false,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
    liteMode: false,
  })
  vi.mocked(fetchSessions).mockReset()
  // Cross-context session-bleed fix: sessionByWorkspace is now mirrored into
  // localStorage (keyed `omnipus.sessionByWorkspace.v1`) so a real reload can
  // tell "this browser has been here before" apart from "brand-new context".
  // Without clearing it here, a workspace id reused across describe blocks
  // in THIS file (most use 'ws-1') would leak an earlier test's persisted
  // decision into a later one — exactly the bleed this fix closes at the
  // browser level, so the unit tests must not reintroduce it at the suite
  // level.
  localStorage.clear()
}

/** Minimal structural echo of session.ts's private WorkspaceSessionDescriptor —
 *  not imported (the interface isn't exported), just structurally compatible. */
type Descriptor = { id: string; type: Session['type']; title: string | null; agentId: string | null }

/**
 * Simulates a real page reload: in-memory Zustand state resets to its
 * cold-boot shape (exactly what a fresh module load produces), but
 * localStorage — and the server — are left untouched. Use this AFTER
 * establishing state via the real public actions (attachToSession,
 * startNewSession, setWorkspaceSessionDescriptor) to test what a genuine
 * reload of THIS SAME browser restores, as opposed to `resetAll()` (which
 * also clears localStorage, simulating a brand-new browser/context).
 */
function simulateReload() {
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
  })
}

/**
 * Establishes "this browser previously attached to `descriptor` in
 * `workspaceId`" (persisting it, via the real setWorkspaceSessionDescriptor
 * action) and then simulates the reload that wipes the in-memory pointer —
 * the exact precondition `enterWorkspaceChat`'s remembered-session branch is
 * built for.
 */
function rememberSessionAcrossReload(workspaceId: string, descriptor: Descriptor | null) {
  useWorkspacesStore.setState({ activeWorkspaceId: workspaceId })
  useSessionStore.getState().setWorkspaceSessionDescriptor(workspaceId, descriptor)
  simulateReload()
  useWorkspacesStore.setState({ activeWorkspaceId: workspaceId })
}

function makeMockConnection() {
  return {
    send: vi.fn().mockReturnValue(true),
    close: vi.fn(),
    isConnected: true,
  }
}

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 'sess-default',
    agent_id: 'mia',
    title: 'A session',
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 1,
    ...overrides,
  }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('sessionByWorkspace — written by attachToSession', () => {
  beforeEach(resetAll)

  it('records the session under the active workspace when WS is connected', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useConnectionStore.setState({ connection: makeMockConnection() as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-abc', 'chat', 'My chat', 'agent-1')

    const map = useSessionStore.getState().sessionByWorkspace
    expect(map['ws-1']).toEqual({
      id: 'sess-abc',
      type: 'chat',
      title: 'My chat',
      agentId: 'agent-1',
    })
  })

  it('records the session under the active workspace when WS is offline', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-2' })
    // No connection

    useSessionStore.getState().attachToSession('sess-offline', 'task', 'Task title', 'agent-2')

    const map = useSessionStore.getState().sessionByWorkspace
    expect(map['ws-2']).toEqual({
      id: 'sess-offline',
      type: 'task',
      title: 'Task title',
      agentId: 'agent-2',
    })
  })

  it('does NOT write to sessionByWorkspace when activeWorkspaceId is null', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useConnectionStore.setState({ connection: makeMockConnection() as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-xyz', 'chat', undefined, undefined)

    expect(Object.keys(useSessionStore.getState().sessionByWorkspace)).toHaveLength(0)
  })
})

describe('sessionByWorkspace — written by startNewSession', () => {
  beforeEach(resetAll)

  it('writes null for the active workspace when starting a new session', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    useSessionStore.getState().startNewSession()

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('does not write when there is no active workspace', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.getState().startNewSession()

    expect(Object.keys(useSessionStore.getState().sessionByWorkspace)).toHaveLength(0)
  })
})

describe('setActiveSession — never records the "__pending" sentinel', () => {
  beforeEach(resetAll)

  it('does NOT write a sessionByWorkspace descriptor when sessionId is "__pending"', () => {
    // BDD: Given a workspace-setup kickoff (or any no-session sendMessage)
    //   activates the local '__pending' placeholder session,
    //   When setActiveSession('__pending', ...) runs,
    //   Then sessionByWorkspace must NOT record it — recording it would make
    //   the next enterWorkspaceChat for this workspace call
    //   attachToSession('__pending'), which the server rejects as an
    //   invalid session_id after the local bucket has already been wiped.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    useSessionStore.getState().setActiveSession('__pending', 'agent-1', 'core')

    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect('ws-1' in useSessionStore.getState().sessionByWorkspace).toBe(false)
  })

  it('still records a real descriptor for a subsequent real session id after a "__pending" call', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    useSessionStore.getState().setActiveSession('__pending', 'agent-1', 'core')
    useSessionStore.getState().setActiveSession('sess-real', 'agent-1', 'core')

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-real',
      type: 'chat',
      title: null,
      agentId: 'agent-1',
    })
  })

  it('does not write a descriptor for sessionId=null either (pre-existing behavior, unaffected)', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    useSessionStore.getState().setActiveSession(null, 'agent-1', 'core')

    expect('ws-1' in useSessionStore.getState().sessionByWorkspace).toBe(false)
  })
})

describe('enterWorkspaceChat — legacy "__pending" descriptor (defense-in-depth)', () => {
  beforeEach(resetAll)

  it('starts fresh instead of attaching when the stored descriptor id is "__pending"', () => {
    // Simulates a descriptor written by some other/legacy path before this
    // fix — enterWorkspaceChat must never call attachToSession('__pending').
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'some-other-session',
      sessionByWorkspace: {
        'ws-1': { id: '__pending', type: 'chat', title: null, agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // startNewSession, not attachToSession — activeSessionId resets to null,
    // never becomes '__pending'.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('is a no-op (activeSessionId stays null) when already null and the stored descriptor is "__pending"', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: {
        'ws-1': { id: '__pending', type: 'chat', title: null, agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })
})

describe('enterWorkspaceChat — first visit / no local descriptor (browser has never decided)', () => {
  beforeEach(resetAll)

  // Cross-context session-bleed fix: `descriptor === undefined` used to
  // ALWAYS ask the server "what's most recent in this workspace" (the
  // original D4 fix), because a cold reload produces the exact same local
  // state (`{}`) as a real first visit. That question is answerable
  // regardless of whether THIS BROWSER has ever opened the workspace, which
  // is exactly what let a brand-new context inherit a totally different
  // browser/tab/e2e-spec's conversation. Now, `undefined` in-memory AND no
  // localStorage entry for the workspace means "this browser has never
  // decided anything here" — land on blank WITHOUT ever asking the server.

  it('stays fresh WITHOUT querying the server when this browser has no persisted decision for the workspace', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    // Simulate a session from a different context being active
    useSessionStore.setState({ activeSessionId: 'other-sess', sessionByWorkspace: {} })

    await useSessionStore.getState().enterWorkspaceChat('ws-1')

    // The defining behavior of this fix: no network round-trip at all.
    expect(fetchSessions).not.toHaveBeenCalled()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // Explicitly recorded as null (not left `undefined`) so a same-tab
    // re-entry doesn't re-derive it every time.
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('is a no-op (activeSessionId stays null) when already null and this browser has no persisted decision', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {} })

    await useSessionStore.getState().enterWorkspaceChat('ws-1')

    expect(fetchSessions).not.toHaveBeenCalled()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it(
    'REGRESSION REPRO: does NOT inherit a session that exists server-side for the workspace when THIS browser never visited it',
    async () => {
      // BDD: Given the SERVER has a real, recently-updated session for
      //   ws-1 (created by a DIFFERENT browser/tab/e2e-spec — the gateway
      //   and workspace are shared; `playwright.config.ts` runs
      //   `workers: 1` against one gateway per CI shard),
      //   When THIS browser — which has no localStorage record of ever
      //   having been in ws-1 — calls enterWorkspaceChat('ws-1') for the
      //   first time,
      //   Then it must land on a blank composer and must NEVER have asked
      //   the server, which previously would have handed back that
      //   session and silently continued a conversation this browser never
      //   started. This is the exact shape of the CI regression across
      //   chat.spec.ts (d), open-in-chat.spec.ts (b),
      //   delegation-hidden.spec.ts, handoff.spec.ts, and subagent.spec.ts.
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
      useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {} })
      vi.mocked(fetchSessions).mockResolvedValue([
        makeSession({
          id: 'sess-from-another-browser',
          workspace_id: 'ws-1',
          updated_at: '2026-07-27T00:00:00Z',
        }),
      ])

      await useSessionStore.getState().enterWorkspaceChat('ws-1')

      expect(fetchSessions).not.toHaveBeenCalled()
      expect(useSessionStore.getState().activeSessionId).toBeNull()
      expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
    },
  )

  it('after landing on fresh once, a SAME-TAB re-entry stays fresh too (no repeated fetch attempts)', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {} })

    await useSessionStore.getState().enterWorkspaceChat('ws-1')
    await useSessionStore.getState().enterWorkspaceChat('ws-1')

    expect(fetchSessions).not.toHaveBeenCalled()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })
})

describe('enterWorkspaceChat — remembered-session restore after reload (browser-scoped fix)', () => {
  beforeEach(resetAll)

  it(
    'REVERT-PROOF: restores the EXACT session this browser was attached to, after a simulated reload',
    async () => {
      // BDD: Given THIS browser previously attached to a real session in
      //   ws-1 (attachToSession — persists the decision to localStorage),
      //   When the page reloads — wiping sessionByWorkspace back to `{}`,
      //   but leaving localStorage and the server-side session intact,
      //   Then entering the workspace must restore that exact session
      //   rather than silently landing on the blank "Select an agent"
      //   Welcome screen (the original D4 defect: 3 UAT testers).
      rememberSessionAcrossReload('ws-1', {
        id: 'sess-restored',
        type: 'chat',
        title: 'Reloaded conversation',
        agentId: 'jim',
      })
      vi.mocked(fetchSessions).mockResolvedValue([
        makeSession({
          id: 'sess-restored',
          type: 'chat',
          title: 'Reloaded conversation',
          agent_id: 'jim',
          workspace_id: 'ws-1',
          updated_at: '2026-07-27T12:00:00Z',
        }),
      ])

      await useSessionStore.getState().enterWorkspaceChat('ws-1')

      expect(useSessionStore.getState().activeSessionId).toBe('sess-restored')
      expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
        id: 'sess-restored',
        type: 'chat',
        title: 'Reloaded conversation',
        agentId: 'jim',
      })
    },
  )

  it(
    'REGRESSION REPRO: an explicit "New chat" survives a reload — never resurrects the session just left',
    async () => {
      // BDD: Given THIS browser attached to a real session in ws-1, then
      //   the user clicked "New chat" (startNewSession — persists `null`),
      //   When the page reloads before the user sends anything in the new,
      //   still-sessionless conversation,
      //   Then entering the workspace must stay fresh and must NEVER ask
      //   the server — which would happily report the OLD session as
      //   "most recently updated" (nothing newer was ever created) and
      //   resurrect exactly the conversation the user walked away from.
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
      useSessionStore.getState().attachToSession('sess-old', 'chat', 'Old conversation', 'jim')
      useSessionStore.getState().startNewSession()
      simulateReload()
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

      vi.mocked(fetchSessions).mockResolvedValue([
        makeSession({
          id: 'sess-old',
          workspace_id: 'ws-1',
          updated_at: '2026-07-27T12:00:00Z',
        }),
      ])

      await useSessionStore.getState().enterWorkspaceChat('ws-1')

      expect(fetchSessions).not.toHaveBeenCalled()
      expect(useSessionStore.getState().activeSessionId).toBeNull()
      expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
    },
  )

  it('sets resolvingSessionForWorkspace while the verification fetch is in flight, and clears it after', async () => {
    rememberSessionAcrossReload('ws-1', { id: 'sess-remembered', type: 'chat', title: null, agentId: 'mia' })
    let resolveFetch: (sessions: Session[]) => void = () => {}
    vi.mocked(fetchSessions).mockReturnValue(
      new Promise((resolve) => { resolveFetch = resolve })
    )

    const pending = useSessionStore.getState().enterWorkspaceChat('ws-1')
    // Microtask tick so the async function body runs up to the `await`.
    await Promise.resolve()
    expect(useSessionStore.getState().resolvingSessionForWorkspace['ws-1']).toBe(true)

    resolveFetch([])
    await pending

    expect(useSessionStore.getState().resolvingSessionForWorkspace['ws-1']).toBeUndefined()
  })

  it('restores the REMEMBERED session by id, NOT simply whichever is most-recently-updated in the workspace', async () => {
    // BDD: two sessions exist for ws-1 server-side — the one THIS browser
    // actually had open (older updated_at) and a newer one that belongs to
    // a different browser/tab. The old "most recent wins" heuristic would
    // pick the wrong one; the fix must match by remembered id.
    rememberSessionAcrossReload('ws-1', { id: 'sess-mine', type: 'chat', title: null, agentId: 'mia' })
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'sess-mine', workspace_id: 'ws-1', updated_at: '2026-01-01T00:00:00Z' }),
      makeSession({ id: 'sess-someone-elses', workspace_id: 'ws-1', updated_at: '2026-07-27T00:00:00Z' }),
    ])

    await useSessionStore.getState().enterWorkspaceChat('ws-1')

    expect(useSessionStore.getState().activeSessionId).toBe('sess-mine')
  })

  it('gracefully falls back to fresh when the remembered session id no longer exists server-side (deleted)', async () => {
    rememberSessionAcrossReload('ws-1', { id: 'sess-deleted-elsewhere', type: 'chat', title: null, agentId: 'mia' })
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'sess-unrelated', workspace_id: 'ws-1', updated_at: '2026-07-27T00:00:00Z' }),
    ])

    await useSessionStore.getState().enterWorkspaceChat('ws-1')

    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('does not clobber a descriptor another code path already wrote while the fetch was in flight', async () => {
    rememberSessionAcrossReload('ws-1', { id: 'sess-remembered', type: 'chat', title: null, agentId: 'mia' })
    let resolveFetch: (sessions: Session[]) => void = () => {}
    vi.mocked(fetchSessions).mockReturnValue(
      new Promise((resolve) => { resolveFetch = resolve })
    )

    const pending = useSessionStore.getState().enterWorkspaceChat('ws-1')
    await Promise.resolve()

    // Something else (e.g. a deep-link route, or the user picking a session
    // from the Sidebar) claims the workspace first.
    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-1', {
      id: 'sess-claimed-first',
      type: 'chat',
      title: 'Claimed by another path',
      agentId: 'ava',
    })

    // The server resolution now lands — must not overwrite the one that was
    // already claimed, even though the remembered id IS in the response.
    resolveFetch([
      makeSession({ id: 'sess-remembered', workspace_id: 'ws-1', updated_at: '2026-07-27T00:00:00Z' }),
    ])
    await pending

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-claimed-first',
      type: 'chat',
      title: 'Claimed by another path',
      agentId: 'ava',
    })
  })

  it('records the descriptor but does NOT attach when the user has navigated to a different workspace before resolution lands', async () => {
    rememberSessionAcrossReload('ws-1', { id: 'sess-ws1-late', type: 'chat', title: null, agentId: 'mia' })
    let resolveFetch: (sessions: Session[]) => void = () => {}
    vi.mocked(fetchSessions).mockReturnValue(
      new Promise((resolve) => { resolveFetch = resolve })
    )

    const pending = useSessionStore.getState().enterWorkspaceChat('ws-1')
    await Promise.resolve()

    // User navigates away to ws-2 before the fetch for ws-1 resolves.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-2' })
    useSessionStore.setState({ activeSessionId: 'sess-ws2-active' })

    resolveFetch([
      makeSession({ id: 'sess-ws1-late', workspace_id: 'ws-1', updated_at: '2026-07-27T00:00:00Z' }),
    ])
    await pending

    // Descriptor recorded for ws-1 so a later re-entry finds it...
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-ws1-late',
      type: 'chat',
      title: 'A session',
      agentId: 'mia',
    })
    // ...but activeSessionId (ws-2's session) must NOT have been clobbered.
    expect(useSessionStore.getState().activeSessionId).toBe('sess-ws2-active')
  })

  it('does not re-fetch while a resolution for the same workspace is already in flight (de-dupe)', async () => {
    rememberSessionAcrossReload('ws-1', { id: 'sess-deduped', type: 'chat', title: null, agentId: 'mia' })
    let resolveFetch: (sessions: Session[]) => void = () => {}
    vi.mocked(fetchSessions).mockReturnValue(
      new Promise((resolve) => { resolveFetch = resolve })
    )

    const first = useSessionStore.getState().enterWorkspaceChat('ws-1')
    await Promise.resolve()
    const second = useSessionStore.getState().enterWorkspaceChat('ws-1')

    resolveFetch([
      makeSession({ id: 'sess-deduped', workspace_id: 'ws-1', updated_at: '2026-07-27T00:00:00Z' }),
    ])
    await Promise.all([first, second])

    expect(fetchSessions).toHaveBeenCalledTimes(1)
    expect(useSessionStore.getState().activeSessionId).toBe('sess-deduped')
  })

  it('gracefully falls back (toast, no crash, stays retryable) when the server fetch fails', async () => {
    useUiStore.setState({ toasts: [] })
    rememberSessionAcrossReload('ws-1', { id: 'sess-remembered', type: 'chat', title: null, agentId: 'mia' })
    vi.mocked(fetchSessions).mockRejectedValue(new Error('network down'))

    await useSessionStore.getState().enterWorkspaceChat('ws-1')

    // No crash, activeSessionId stays null (Welcome renders).
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // Left `undefined` (NOT `null`) — retryable on the next enterWorkspaceChat,
    // unlike the genuinely-empty case which is permanently `null`.
    expect('ws-1' in useSessionStore.getState().sessionByWorkspace).toBe(false)
    // User-visible feedback, not a silent failure.
    const toasts = useUiStore.getState().toasts
    expect(toasts.length).toBeGreaterThan(0)
    expect(toasts.some((t) => /restore|conversation/i.test(t.message))).toBe(true)
  })
})

describe('enterWorkspaceChat — explicit fresh (descriptor is null)', () => {
  beforeEach(resetAll)

  it('clears active session when the workspace was previously freshly started and session is active', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'some-session',
      sessionByWorkspace: { 'ws-1': null },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // startNewSession clears activeSessionId
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('is a no-op (activeSessionId stays null) when already null and workspace explicitly freshly started', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-fresh' })
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: { 'ws-fresh': null },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-fresh')

    // Still null, no change
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // The descriptor for ws-fresh stays null (not changed by a no-op)
    expect(useSessionStore.getState().sessionByWorkspace['ws-fresh']).toBeNull()
  })
})

describe('enterWorkspaceChat — restore stored session', () => {
  beforeEach(resetAll)

  it('attaches to the stored session when descriptor.id differs from activeSessionId', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    // No WS connection — uses the offline path of attachToSession
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: {
        'ws-1': { id: 'prev-sess', type: 'chat', title: 'Previous chat', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // attachToSession (offline path) sets activeSessionId to the stored session's id
    expect(useSessionStore.getState().activeSessionId).toBe('prev-sess')
  })

  it('is a no-op when descriptor.id already matches activeSessionId (Bug 1: tab switch within workspace)', () => {
    // BDD: Given workspace ws-1 with session sess-123 active,
    //   When the user switches to the Board tab and back to Chat
    //   (enterWorkspaceChat fires again),
    //   Then neither startNewSession nor attachToSession is called —
    //   activeSessionId stays 'sess-123'.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'sess-123',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-123', type: 'chat', title: 'Still here', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // Session must be preserved — no change
    expect(useSessionStore.getState().activeSessionId).toBe('sess-123')
    // sessionByWorkspace must not be mutated
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-123',
      type: 'chat',
      title: 'Still here',
      agentId: 'agent-1',
    })
  })
})

describe('enterWorkspaceChat — workspace switch restores correct session', () => {
  beforeEach(resetAll)

  it('switches to ws-2 stored session when entering ws-2', () => {
    // No WS connection — offline path
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-2' })
    useSessionStore.setState({
      activeSessionId: 'sess-ws1',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-ws1', type: 'chat', title: 'WS1 chat', agentId: 'mia' },
        'ws-2': { id: 'sess-ws2', type: 'chat', title: 'WS2 chat', agentId: 'jim' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-2')

    // Should be on ws-2's session now
    expect(useSessionStore.getState().activeSessionId).toBe('sess-ws2')
  })
})

describe('setWorkspaceSessionDescriptor — explicit descriptor write', () => {
  beforeEach(resetAll)

  it('writes the descriptor under the given workspaceId regardless of activeWorkspaceId', () => {
    // BDD: Given activeWorkspaceId is null (user not currently on a workspace),
    //   When setWorkspaceSessionDescriptor is called with an explicit wsId and descriptor,
    //   Then sessionByWorkspace[wsId] holds that descriptor.
    //
    // This is the race-free handoff: the deep-link session route writes the
    // descriptor by explicit key BEFORE navigate(), so WorkspaceTabContainer's
    // enterWorkspaceChat fires AFTER the descriptor is already in place.
    useWorkspacesStore.setState({ activeWorkspaceId: null })

    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-1', {
      id: 'sess-deep-link',
      type: 'chat',
      title: 'Deep-linked chat',
      agentId: 'mia',
    })

    const descriptor = useSessionStore.getState().sessionByWorkspace['ws-1']
    expect(descriptor).toEqual({
      id: 'sess-deep-link',
      type: 'chat',
      title: 'Deep-linked chat',
      agentId: 'mia',
    })
  })

  it('enterWorkspaceChat is a no-op after setWorkspaceSessionDescriptor when activeSessionId matches', () => {
    // BDD: Given a deep-link route that:
    //   1. Sets activeSessionId to A (via setActiveSession or attachToSession)
    //   2. Calls setWorkspaceSessionDescriptor('ws-1', { id: A, ... })
    //   3. Navigates to /workspaces/ws-1/chat
    //   When WorkspaceTabContainer mounts and calls enterWorkspaceChat('ws-1'),
    //   Then descriptor.id === activeSessionId → no-op (never calls startNewSession).
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.setState({ activeSessionId: 'sess-A' })

    // Step 2: deep-link route writes the descriptor before navigate.
    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-1', {
      id: 'sess-A',
      type: 'chat',
      title: null,
      agentId: 'mia',
    })

    // Update workspace (simulating setActiveWorkspaceId call in the route effect).
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    // Step 3: enterWorkspaceChat fires on WorkspaceTabContainer mount.
    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // activeSessionId must remain 'sess-A' — no startNewSession fired.
    expect(useSessionStore.getState().activeSessionId).toBe('sess-A')
    // The descriptor must be unchanged.
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-A',
      type: 'chat',
      title: null,
      agentId: 'mia',
    })
  })

  it('can write null to mark a workspace as fresh (used by startNewSession callers)', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-5' })
    useSessionStore.setState({
      sessionByWorkspace: {
        'ws-5': { id: 'old-sess', type: 'chat', title: null, agentId: null },
      },
    })

    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-5', null)

    expect(useSessionStore.getState().sessionByWorkspace['ws-5']).toBeNull()
  })

  it('preserves other workspace descriptors when writing for a specific workspace', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.setState({
      sessionByWorkspace: {
        'ws-other': { id: 'sess-other', type: 'chat', title: 'Other', agentId: 'jim' },
      },
    })

    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-new', {
      id: 'sess-new',
      type: 'task',
      title: 'My task',
      agentId: 'ava',
    })

    expect(useSessionStore.getState().sessionByWorkspace['ws-other']).toEqual({
      id: 'sess-other',
      type: 'chat',
      title: 'Other',
      agentId: 'jim',
    })
    expect(useSessionStore.getState().sessionByWorkspace['ws-new']).toEqual({
      id: 'sess-new',
      type: 'task',
      title: 'My task',
      agentId: 'ava',
    })
  })
})

describe('setActiveSession — session type preservation (Wave-1 Bug 1 regression)', () => {
  beforeEach(resetAll)

  it('preserves the pre-existing attachedSessionType in sessionByWorkspace instead of falling back to "chat"', () => {
    // BDD: Given attachedSessionType is 'task' (set by a prior attachToSession/
    //   setAttachedContext call for the currently active session),
    //   When setActiveSession is called to switch the active agent while the
    //   session stays conceptually the same (e.g. ChatControls agent picker),
    //   Then the sessionByWorkspace descriptor must record 'task', not 'chat' —
    //   the second set() must not read attachedSessionType AFTER the first
    //   set() has already nulled it out.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachedSessionType: 'task', attachedTaskTitle: 'My task' })

    useSessionStore.getState().setActiveSession('sess-task-1', 'agent-1', null)

    const descriptor = useSessionStore.getState().sessionByWorkspace['ws-1']
    expect(descriptor?.type).toBe('task')
    expect(descriptor?.id).toBe('sess-task-1')
    // Same stale-read hazard as attachedSessionType above, one field below:
    // the second set()'s updater must use the pre-captured priorTaskTitle,
    // not state.attachedTaskTitle (which the first set() already nulled).
    expect(descriptor?.title).toBe('My task')
  })

  it('falls back to "chat" when there was no prior attachedSessionType', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachedSessionType: null, attachedTaskTitle: null })

    useSessionStore.getState().setActiveSession('sess-plain', 'agent-1', null)

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']?.type).toBe('chat')
  })

  it('still resets attachedSessionType/attachedTaskTitle on the store itself for the new session', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachedSessionType: 'channel', attachedTaskTitle: 'Old title' })

    useSessionStore.getState().setActiveSession('sess-new', 'agent-1', null)

    // The live attachedSessionType/attachedTaskTitle fields are still cleared
    // (unrelated to the sessionByWorkspace bug) — only the *captured* value
    // used for the sessionByWorkspace descriptor is preserved.
    expect(useSessionStore.getState().attachedSessionType).toBeNull()
  })
})

describe('attachToSession — no bucket wipe on failed send (Wave-1 Bug 2 regression)', () => {
  beforeEach(resetAll)

  it('does NOT reset the chat bucket when connection.send returns false', () => {
    const resetSpy = vi.fn()
    registerChatResetForReplay(resetSpy)

    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    const failingConnection = { send: vi.fn().mockReturnValue(false), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: failingConnection as never, isConnected: true })

    const result = useSessionStore.getState().attachToSession('sess-fail', 'chat', 'Title', 'agent-1')

    expect(resetSpy).not.toHaveBeenCalled()
    // Connection error must be surfaced.
    expect(useConnectionStore.getState().connectionError).toMatch(/Could not attach/)
    // activeSessionId must NOT have been switched to the failed session.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // Round-2 fix: the return value is the caller-facing success signal —
    // useSelectSession (and other callers) key their abort logic off this.
    expect(result).toBe(false)
  })

  it('DOES reset the chat bucket once send is confirmed successful', () => {
    const resetSpy = vi.fn()
    registerChatResetForReplay(resetSpy)

    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    const okConnection = { send: vi.fn().mockReturnValue(true), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: okConnection as never, isConnected: true })

    const result = useSessionStore.getState().attachToSession('sess-ok', 'chat', 'Title', 'agent-1')

    expect(resetSpy).toHaveBeenCalledWith('sess-ok')
    expect(useSessionStore.getState().activeSessionId).toBe('sess-ok')
    expect(result).toBe(true)
  })
})

describe('attachToSession — WS-down user feedback (fix 6)', () => {
  beforeEach(() => {
    resetAll()
    useUiStore.setState({ toasts: [] })
  })

  it('shows a user-visible toast when there is no WS connection (previously console.warn only)', () => {
    // No connection set up — attachToSession takes the offline branch.
    const result = useSessionStore.getState().attachToSession('sess-offline-toast', 'chat', 'Title', 'agent-1')

    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].message.toLowerCase()).toMatch(/not connected|connection/)
    expect(toasts[0].variant).toBe('warning')
    // Offline is a "recorded, will finish later" state, not a failure — the
    // caller should proceed (seed tokens / navigate), unlike the send-false
    // branch above which returns false.
    expect(result).toBe(true)
  })

  it('does NOT show a toast when the WS is connected and the attach succeeds', () => {
    const okConnection = { send: vi.fn().mockReturnValue(true), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: okConnection as never, isConnected: true })

    const result = useSessionStore.getState().attachToSession('sess-online', 'chat', 'Title', 'agent-1')

    expect(useUiStore.getState().toasts).toHaveLength(0)
    expect(result).toBe(true)
  })

  it('still records activeSessionId (offline path is otherwise unchanged) alongside the new toast', () => {
    useSessionStore.getState().attachToSession('sess-offline-toast-2', 'task', 'Task Title', 'agent-2')

    expect(useSessionStore.getState().activeSessionId).toBe('sess-offline-toast-2')
    expect(useSessionStore.getState().attachedSessionType).toBe('task')
    expect(useUiStore.getState().toasts).toHaveLength(1)
  })
})

describe('pruneSessionDescriptor — round-2 fix: no zombie reattach after delete', () => {
  beforeEach(resetAll)

  it('nulls the sessionByWorkspace entry for a deleted session so entering that workspace does not reattach the dead id', () => {
    // BDD: Given workspace W's stored descriptor points at session S,
    //   When S is deleted (pruneSessionDescriptor(S) is called, e.g. from
    //   SearchModal's deleteMut.onSuccess),
    //   Then entering W afterwards must NOT attach S — it's gone.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'some-other-session',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-deleted', type: 'chat', title: 'Deleted', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-deleted')

    // The descriptor is nulled ("explicitly fresh"), not left pointing at the
    // dead id.
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()

    // Entering ws-1 now must start fresh, never attach the dead session id.
    useSessionStore.getState().enterWorkspaceChat('ws-1')
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('clears activeSessionId when the deleted session is the currently-attached one', () => {
    useSessionStore.setState({
      activeSessionId: 'sess-current',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-current', type: 'chat', title: 'Current', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-current')

    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('does NOT touch activeSessionId when the deleted session is not the active one', () => {
    useSessionStore.setState({
      activeSessionId: 'sess-active',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-other', type: 'chat', title: 'Other', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-other')

    expect(useSessionStore.getState().activeSessionId).toBe('sess-active')
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('preserves descriptors for OTHER workspaces untouched', () => {
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: {
        'ws-1': { id: 'sess-deleted', type: 'chat', title: 'Deleted', agentId: 'agent-1' },
        'ws-2': { id: 'sess-alive', type: 'task', title: 'Alive', agentId: 'agent-2' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-deleted')

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
    expect(useSessionStore.getState().sessionByWorkspace['ws-2']).toEqual({
      id: 'sess-alive',
      type: 'task',
      title: 'Alive',
      agentId: 'agent-2',
    })
  })
})
