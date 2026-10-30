/**
 * RED: a live open_in_library frame opens the Library when this tab is watching
 * (BS-6.1). decideLibraryOpen is loaded dynamically so a missing module fails
 * this file with BLOCKED rather than a collection error (BS-6.5 / FR-018).
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from '@/store/chat'
import { registerChatView } from '@/store/chatView'
import { resetLibraryOpenState, useLibraryOpenStore } from '@/store/chat/libraryOpen'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

const REQUEST = '0123456789abcdef0123456789abcdef'

type Decide = (input: {
  tabWatching: boolean
  guardPending: boolean
  libraryDirty: boolean
  replaying: boolean
  awaitingCatchUp: boolean
  hasSeq: boolean
  requestId: string
  duplicate: boolean
  historyPushed: boolean
  panelRowWidth: number
}) => 'open' | 'notice' | 'drop'

async function loadDecide(): Promise<Decide> {
  try {
    const spec = './decideLibraryOpen'
    const mod = await import(/* @vite-ignore */ spec) as { decideLibraryOpen?: Decide }
    if (typeof mod.decideLibraryOpen !== 'function') {
      throw new Error('missing export')
    }
    return mod.decideLibraryOpen
  } catch (err) {
    throw new Error(
      `BLOCKED: decideLibraryOpen not implemented — required by FR-018 / BS-6.5 (${err instanceof Error ? err.message : String(err)})`,
    )
  }
}

describe('decideLibraryOpen', () => {
  it('opens only when this tab is watching and the Library can move (FR-018)', async () => {
    const decide = await loadDecide()
    const base = {
      tabWatching: true,
      guardPending: false,
      libraryDirty: false,
      replaying: false,
      awaitingCatchUp: false,
      hasSeq: false,
      requestId: REQUEST,
      duplicate: false,
      historyPushed: false,
      panelRowWidth: 0,
    }
    expect(decide(base)).toBe('open')
    expect(decide({ ...base, tabWatching: false })).toBe('notice')
    expect(decide({ ...base, guardPending: true })).toBe('notice')
    expect(decide({ ...base, libraryDirty: true })).toBe('notice')
    expect(decide({ ...base, replaying: true })).toBe('drop')
    expect(decide({ ...base, awaitingCatchUp: true })).toBe('drop')
    expect(decide({ ...base, hasSeq: true })).toBe('drop')
    expect(decide({ ...base, duplicate: true })).toBe('drop')
    expect(decide({ ...base, requestId: 'NOT-HEX' })).toBe('drop')
    // Spec replacement (team-lead ruling): sessionOnScreen left decideLibraryOpen in 21a75ff2c; an off-screen chat is tabWatching false → notice, not a separate input.
    // Founder decision supersedes the phone-width gate. Width and historyPushed
    // must not change an otherwise-open decision.
    expect(decide({ ...base, panelRowWidth: 0 })).toBe('open')
    expect(decide({ ...base, panelRowWidth: 679 })).toBe('open')
    expect(decide({ ...base, historyPushed: true })).toBe('open')
  })
})

describe('open_in_library frame', () => {
  let releaseView: (() => void) | undefined
  beforeEach(() => {
    useUiStore.getState().closePanel()
    useSessionStore.getState().setActiveSession('sess-1', 'mia')
    useChatStore.setState({ sessionsById: { 'sess-1': emptySessionState() } })
    // Watching (spec "Chat view active") needs the session on screen and a
    // chat view for it mounted. Setting the active session alone is not enough.
    releaseView = registerChatView('sess-1', 'W')
  })
  afterEach(() => {
    releaseView?.()
    releaseView = undefined
    resetLibraryOpenState()
  })

  it('opens the Library at the frame path when this chat is on screen (BS-6.1)', () => {
    useChatStore.getState().handleFrame({
      type: 'open_in_library',
      session_id: 'sess-1',
      call_id: 'call-1',
      request_id: REQUEST,
      workspace_id: 'W',
      path: 'notes/plan.md',
    } as never)
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'notes/plan.md' },
    })
  })

  it('stores a notice for a session that is not on screen and does not open the Library (BS-6.5)', () => {
    resetLibraryOpenState()
    useChatStore.getState().handleFrame({
      type: 'open_in_library',
      session_id: 'sess-away',
      call_id: 'call-away',
      request_id: REQUEST,
      workspace_id: 'W',
      path: 'notes/plan.md',
    } as never)
    expect(useUiStore.getState().activePanel?.id).not.toBe('library')
    expect(useLibraryOpenStore.getState().notices['sess-away']).toEqual({
      workspaceId: 'W',
      path: 'notes/plan.md',
      requestId: REQUEST,
    })
  })

  function frame(sessionId: string, requestId: string, extra: Record<string, unknown> = {}) {
    return {
      type: 'open_in_library',
      session_id: sessionId,
      call_id: 'call-1',
      request_id: requestId,
      workspace_id: 'W',
      path: 'notes/plan.md',
      ...extra,
    } as never
  }

  function hex(n: number): string {
    return n.toString(16).padStart(32, '0')
  }

  it('drops a frame while this session is replaying, with no notice and no announcement (FR-017)', () => {
    const before = useLibraryOpenStore.getState().announcement
    useChatStore.setState({
      sessionsById: { 'sess-1': { ...emptySessionState(), isReplaying: true } },
    })
    useChatStore.getState().handleFrame(frame('sess-1', REQUEST))
    expect(useUiStore.getState().activePanel).toBeNull()
    expect(useLibraryOpenStore.getState().notices['sess-1']).toBeUndefined()
    expect(useLibraryOpenStore.getState().announcement).toEqual(before)
  })

  it('drops a frame while this session is awaiting catch-up, with no notice (BS-6.7)', () => {
    useChatStore.setState({
      sessionsById: { 'sess-1': { ...emptySessionState(), awaitingCatchUp: true } },
    })
    useChatStore.getState().handleFrame(frame('sess-1', REQUEST))
    expect(useUiStore.getState().activePanel?.id).not.toBe('library')
    expect(useLibraryOpenStore.getState().notices['sess-1']).toBeUndefined()
  })

  it('drops a hidden tab instead of opening, and does not open when the tab becomes visible later (BS-6.5)', () => {
    const descriptor = Object.getOwnPropertyDescriptor(document, 'visibilityState')
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' })
    try {
      useChatStore.getState().handleFrame(frame('sess-1', REQUEST))
      expect(useUiStore.getState().activePanel?.id).not.toBe('library')
      expect(useLibraryOpenStore.getState().notices['sess-1']).toEqual({
        workspaceId: 'W',
        path: 'notes/plan.md',
        requestId: REQUEST,
      })
    } finally {
      Object.defineProperty(document, 'visibilityState', descriptor ?? { configurable: true, get: () => 'visible' })
    }
    expect(useUiStore.getState().activePanel?.id).not.toBe('library')
  })

  it('acts once per request id, and a second request id on the same call acts too (BS-6.8)', () => {
    useChatStore.getState().handleFrame(frame('sess-1', REQUEST))
    const announced = useLibraryOpenStore.getState().announcement?.id
    useChatStore.getState().handleFrame(frame('sess-1', REQUEST))
    expect(useLibraryOpenStore.getState().announcement?.id).toBe(announced)
    const other = hex(2)
    useChatStore.getState().handleFrame(frame('sess-1', other))
    expect(useLibraryOpenStore.getState().announcement?.id).not.toBe(announced)
    useUiStore.getState().closePanel()
    useChatStore.getState().handleFrame(frame('sess-other', REQUEST))
    expect(useLibraryOpenStore.getState().notices['sess-other']?.requestId).toBe(REQUEST)
  })

  it('forgets the oldest handled request after 256 others (BS-6.8)', () => {
    for (let n = 1; n <= 256; n += 1) {
      useChatStore.getState().handleFrame(frame('sess-away', hex(n)))
    }
    expect(useLibraryOpenStore.getState().notices['sess-away']?.requestId).toBe(hex(256))
    useChatStore.getState().handleFrame(frame('sess-away', hex(1)))
    expect(useLibraryOpenStore.getState().notices['sess-away']?.requestId).toBe(hex(256))
    useChatStore.getState().handleFrame(frame('sess-away', hex(257)))
    expect(useLibraryOpenStore.getState().notices['sess-away']?.requestId).toBe(hex(257))
    useChatStore.getState().handleFrame(frame('sess-away', hex(1)))
    expect(useLibraryOpenStore.getState().notices['sess-away']?.requestId).toBe(hex(1))
  })

  it('drops a sequenced frame and does not announce it (SEC-8)', () => {
    useChatStore.getState().handleFrame(frame('sess-1', REQUEST, { seq: 1 }))
    expect(useUiStore.getState().activePanel?.id).not.toBe('library')
    expect(useLibraryOpenStore.getState().notices['sess-1']).toBeUndefined()
    expect(useLibraryOpenStore.getState().announcement).toBeNull()
  })
})

describe('useChatPresence mount sites', () => {
  it('is mounted from the three chat views and not from ChatScreen (FR-026)', () => {
    const root = resolve(__dirname, '../../..')
    const sites = [
      'src/routes/_app/sessions.$sessionId.tsx',
      'src/components/workspaces/WorkspaceChatTab.tsx',
      'src/components/chat/OmnipusChatScreen.tsx',
    ]
    for (const rel of sites) {
      const body = readFileSync(resolve(root, rel), 'utf8')
      expect(body, rel).toContain('useChatPresence(')
    }
    const screen = readFileSync(resolve(root, 'src/components/chat/ChatScreen.tsx'), 'utf8')
    expect(screen).not.toContain('useChatPresence(')
  })
})
