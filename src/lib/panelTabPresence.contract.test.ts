// panelTabPresence.contract.test.ts — side-panel-shell-spec.md §12 #14
// (§8.3 already-open-tab contract). No production module implements the
// handle registry + continuous presence list (verified: libraryHandoff and
// browserLiveHandoff are per-feature broadcasts, not this contract). RED
// fails LOUDLY with BLOCKED until the module lands — never a skip.
//
// Oracles (§8.3): identity is panelId×workspace (app at the root) or
// panelId×session×agent for Browser, matched on CURRENT scope (MAJ-213);
// an open handle is focused and window.open is NOT called again (MAJ-202);
// a closed handle is dropped; a presence hit with no handle shows the
// affordance and does not open a docked panel or a new tab (SP-18); a
// blocked popup is fail-visible (MAJ-209); presence payloads never carry
// paths, selections or session contents (MIN-207).

import { describe, expect, it } from 'vitest'

const MODULE_PATH = '@/lib/panelTabPresence'

type Identity = {
  panelId: string
  workspaceId?: string
  sessionId?: string
  agentId?: string
}

type Handle = { closed: boolean; focus: () => void }

type Outcome =
  | { kind: 'focused' }
  | { kind: 'affordance' }
  | { kind: 'opened' }
  | { kind: 'blocked' }

type Api = {
  panelIdentityKey: (id: Identity) => string
  resolvePanelOpen: (input: {
    identity: Identity
    handles: ReadonlyMap<string, Handle>
    presence: Identity[]
    open: () => Window | null
  }) => Outcome
  acceptPresence: (message: unknown) => boolean
}

async function load(): Promise<Api> {
  let mod: unknown
  try {
    mod = await import(/* @vite-ignore */ MODULE_PATH)
  } catch (importErr) {
    throw new Error(
      `BLOCKED: already-open-tab module not implemented — required by side-panel-shell-spec.md §8.3/§12 test #14 (import of ${MODULE_PATH} failed: ${importErr instanceof Error ? importErr.message : String(importErr)})`,
    )
  }
  const api = mod as Partial<Api>
  if (
    typeof api.panelIdentityKey !== 'function' ||
    typeof api.resolvePanelOpen !== 'function' ||
    typeof api.acceptPresence !== 'function'
  ) {
    throw new Error(
      `BLOCKED: ${MODULE_PATH} is missing panelIdentityKey / resolvePanelOpen / acceptPresence — required by side-panel-shell-spec.md §8.3/§12 test #14`,
    )
  }
  return api as Api
}

describe('already-open tab (§12 #14, §8.3)', () => {
  it('exposes the contract module (missing module fails loudly, never skips)', async () => {
    let blocked = ''
    try {
      await load()
    } catch (err) {
      blocked = err instanceof Error ? err.message : String(err)
    }
    expect(blocked, blocked || 'module must exist').toBe('')
  })

  it('identity keys: library is panel×workspace (app at the root); browser is panel×session×agent (MAJ-201)', async () => {
    const { panelIdentityKey } = await load()
    expect(panelIdentityKey({ panelId: 'library', workspaceId: 'ws-1' })).toBe('library:ws-1')
    expect(panelIdentityKey({ panelId: 'library' })).toBe('library:app')
    expect(panelIdentityKey({ panelId: 'browser', sessionId: 's1', agentId: 'a1' })).toBe(
      'browser:s1:a1',
    )
  })

  it('an open handle is focused and window.open is NOT called (MAJ-202, SP-30)', async () => {
    const { panelIdentityKey, resolvePanelOpen } = await load()
    const identity = { panelId: 'library', workspaceId: 'ws-1' }
    const focus = () => {}
    let opened = 0
    const outcome = resolvePanelOpen({
      identity,
      handles: new Map([[panelIdentityKey(identity), { closed: false, focus }]]),
      presence: [],
      open: () => {
        opened += 1
        return null
      },
    })
    expect(outcome).toEqual({ kind: 'focused' })
    expect(opened).toBe(0)
  })

  it('a closed handle is dropped and the open proceeds (stale handle)', async () => {
    const { panelIdentityKey, resolvePanelOpen } = await load()
    const identity = { panelId: 'library', workspaceId: 'ws-1' }
    const outcome = resolvePanelOpen({
      identity,
      handles: new Map([[panelIdentityKey(identity), { closed: true, focus: () => {} }]]),
      presence: [],
      open: () => ({ closed: false }) as unknown as Window,
    })
    expect(outcome).toEqual({ kind: 'opened' })
  })

  it('a presence hit with no handle shows the affordance and does not open (SP-18)', async () => {
    const { resolvePanelOpen } = await load()
    let opened = 0
    const outcome = resolvePanelOpen({
      identity: { panelId: 'library', workspaceId: 'ws-1' },
      handles: new Map(),
      presence: [{ panelId: 'library', workspaceId: 'ws-1' }],
      open: () => {
        opened += 1
        return null
      },
    })
    expect(outcome).toEqual({ kind: 'affordance' })
    expect(opened).toBe(0)
  })

  it('a tab that navigated from workspace A to B matches B only (MAJ-213)', async () => {
    const { resolvePanelOpen } = await load()
    const outcome = resolvePanelOpen({
      identity: { panelId: 'library', workspaceId: 'ws-a' },
      handles: new Map(),
      presence: [{ panelId: 'library', workspaceId: 'ws-b' }],
      open: () => ({ closed: false }) as unknown as Window,
    })
    expect(outcome).toEqual({ kind: 'opened' })
  })

  it('a blocked popup is fail-visible — kind blocked, no silent open (MAJ-209)', async () => {
    const { resolvePanelOpen } = await load()
    const outcome = resolvePanelOpen({
      identity: { panelId: 'library', workspaceId: 'ws-1' },
      handles: new Map(),
      presence: [],
      open: () => null,
    })
    expect(outcome).toEqual({ kind: 'blocked' })
  })

  it('presence messages carrying a path, folder or selection are rejected (MIN-207)', async () => {
    const { acceptPresence } = await load()
    expect(acceptPresence({ panelId: 'library', workspaceId: 'ws-1' })).toBe(true)
    expect(acceptPresence({ panelId: 'library', workspaceId: 'ws-1', path: '/secret' })).toBe(false)
    expect(acceptPresence({ panelId: 'library', folder: 'notes' })).toBe(false)
    expect(acceptPresence({ panelId: 'browser', sessionId: 's1', agentId: 'a1', selection: 'x' })).toBe(
      false,
    )
  })
})
