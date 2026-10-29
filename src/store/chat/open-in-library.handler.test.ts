/**
 * RED: a live open_in_library frame opens the Library when this tab is watching
 * (BS-6.1). decideLibraryOpen is loaded dynamically so a missing module fails
 * this file with BLOCKED rather than a collection error (BS-6.5 / FR-018).
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

const REQUEST = '0123456789abcdef0123456789abcdef'

type Decide = (input: {
  tabWatching: boolean
  guardPending: boolean
  libraryDirty: boolean
  sessionOnScreen: boolean
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
    const mod = await import('./decideLibraryOpen') as { decideLibraryOpen?: Decide }
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
      sessionOnScreen: true,
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
    expect(decide({ ...base, hasSeq: true })).toBe('drop')
    expect(decide({ ...base, duplicate: true })).toBe('drop')
    expect(decide({ ...base, requestId: 'NOT-HEX' })).toBe('drop')
    // Founder decision supersedes the phone-width gate. Width and historyPushed
    // must not change an otherwise-open decision.
    expect(decide({ ...base, panelRowWidth: 0 })).toBe('open')
    expect(decide({ ...base, panelRowWidth: 679 })).toBe('open')
    expect(decide({ ...base, historyPushed: true })).toBe('open')
  })
})

describe('open_in_library frame', () => {
  beforeEach(() => {
    useUiStore.getState().closePanel()
    useSessionStore.getState().setActiveSession('sess-1', 'mia')
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
