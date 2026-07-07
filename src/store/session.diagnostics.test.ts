// session.diagnostics.test.ts — Wave 3 Fix 2: production telemetry coverage
// for session.ts's use of the shared `logDiagnostic` helper (src/lib/telemetry.ts,
// also used by src/store/chat.ts) at its 2 call sites:
// `sessionSetChatReplayingUnregistered`, `sessionAttachToSessionNoConnection`).
//
// Mirrors the established pattern in src/lib/api.test.ts (~L1596-1628) and
// src/store/chatPreferences.test.ts (~L108-124): force the production gate
// open (DEV=false, MODE='production'), trigger the diagnostic condition,
// assert the expected event name + fields were emitted.
//
// Assertions spy on console.error (the same technique src/lib/telemetry.test.ts
// uses for logError itself) rather than telemetry.logError: session.ts calls
// the shared telemetry.logDiagnostic helper, whose own logError call is a
// same-module reference inside src/lib/telemetry.ts — vi.spyOn(telemetry,
// 'logError') cannot intercept a same-module call, only calls made by OTHER
// modules through the imported binding.
//
// IMPORTANT: this file deliberately does NOT import './chat' (or anything
// that transitively imports it). session.ts's `setChatReplaying` only warns
// and records `sessionSetChatReplayingUnregistered` when chat.ts's
// `registerChatSetReplaying` callback has never run in this module's graph —
// importing chat.ts here would register a real callback (a side effect of
// creating useChatStore) and make that branch permanently unreachable.
// Vitest's `pool: 'forks'` (vite.config.ts) isolates each test file into its
// own module registry, so this file staying chat.ts-free is what keeps the
// unregistered branch reachable.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'

function resetAll() {
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
  })
  useConnectionStore.setState({
    connection: null,
    isConnected: false,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
    liteMode: false,
  })
  useWorkspacesStore.setState({
    activeWorkspaceId: null,
    activeMilestoneId: null,
    boardAltitude: 'top-level',
  })
}

describe('session store — production telemetry (Wave 3 Fix 2)', () => {
  beforeEach(() => {
    resetAll()
    vi.restoreAllMocks()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('sessionAttachToSessionNoConnection: attachToSession with no WS connection calls logError with sessionId + type', () => {
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    useSessionStore.getState().attachToSession('sess-no-conn', 'chat', 'Title', 'agent-1')

    expect(errorSpy).toHaveBeenCalledTimes(1)
    const payload = JSON.parse(errorSpy.mock.calls[0][0] as string)
    expect(payload.event).toBe('sessionAttachToSessionNoConnection')
    expect(payload.sessionId).toBe('sess-no-conn')
    expect(payload.type).toBe('chat')
  })

  it('sessionSetChatReplayingUnregistered: attachToSession over a working connection calls logError when chat.ts never registered setChatReplaying', () => {
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const okConnection = { send: vi.fn().mockReturnValue(true), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: okConnection as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-ok', 'chat', 'Title', 'agent-1')

    expect(errorSpy).toHaveBeenCalledTimes(1)
    const payload = JSON.parse(errorSpy.mock.calls[0][0] as string)
    expect(payload.event).toBe('sessionSetChatReplayingUnregistered')
    expect(payload.value).toBe(true)
  })

  it('does NOT call logError in DEV/test builds — production/DEV split preserved', () => {
    // Regression guard: under the default test-mode gate (no env stubbing),
    // the shared `logDiagnostic` helper's (src/lib/telemetry.ts) own
    // `!DEV && MODE !== 'test'` gate stays closed.
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    useSessionStore.getState().attachToSession('sess-no-conn', 'chat', 'Title', 'agent-1')

    expect(errorSpy).not.toHaveBeenCalled()
  })
})
