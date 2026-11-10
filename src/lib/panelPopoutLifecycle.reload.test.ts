import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PanelPopoutRegistration } from './panelPopoutLifecycle'

/**
 * Oracle: side-panel-shell-spec.md §8.3, MAJ-002/MAJ-202. Source reload loses
 * its handle, NOT its child. Presence/Switch must remain possible afterwards.
 * The source pagehide listener and last-owner release are the real production
 * paths. This is a MODULE test, not browser E2E or proof of a visible Switch.
 *
 * REAL: owner counting, infrastructure, registration, handle registry, identity
 * resolution, source event dispatch, close watcher and onClosed delivery.
 * FAKE: only the Window returned by window.open. Its readonly closed getter
 * changes only when close() is actually called; it cannot be forced back open.
 * No lifecycle/presence mocks, injected messages or fake timers.
 *
 * Cases: workspace/session identity classes; source departure versus explicit
 * child close; zero/one/two owner boundary; unrelated identity negative control.
 * Fresh CHECK probes (deferred): close a child on pagehide; close on last-owner
 * release; remove normal child-close detection. GREEN/mutation are not RED work.
 * Gaps: real reload, child authentication/content, BroadcastChannel presence,
 * visible Switch, no duplicate/dock, and post-close toggle need isolated E2E.
 */

// not-wire-format: local test inputs for the public SPA lifecycle API.
type ScopeCase = {
  label: string
  popoutId: string
  identity: PanelPopoutRegistration['identity']
  context: PanelPopoutRegistration['context']
  unknownIdentity: PanelPopoutRegistration['identity']
}

const scopeCases: ScopeCase[] = [
  {
    label: 'workspace Tasks',
    popoutId: 'tasks-reload-child',
    identity: { panelId: 'tasks', workspaceId: 'workspace-a' },
    context: { workspaceId: 'workspace-a' },
    unknownIdentity: { panelId: 'tasks', workspaceId: 'workspace-never-opened' },
  },
  {
    label: 'session Browser',
    popoutId: 'browser-reload-child',
    identity: { panelId: 'browser', sessionId: 'session-a', agentId: 'agent-a' },
    context: { sessionId: 'session-a', agentId: 'agent-a' },
    unknownIdentity: { panelId: 'browser', sessionId: 'session-never-opened', agentId: 'agent-a' },
  },
]

const discardChildren: Array<() => void> = []
const stopOwners: Array<() => void> = []

afterEach(() => {
  // Public discard releases the test's registrations without manufacturing a
  // child-close event. Then release every owner, including already stopped ones.
  for (const discard of discardChildren.splice(0)) discard()
  for (const stop of stopOwners.splice(0)) stop()
  vi.restoreAllMocks()
  vi.resetModules()
})

function openBoundaryChild() {
  let closedByCall = false
  const close = vi.fn(() => { closedByCall = true })
  const focus = vi.fn()
  const handle = {
    get closed() { return closedByCall },
    close,
    focus,
  } as unknown as Window
  const open = vi.spyOn(window, 'open').mockReturnValue(handle)
  const returned = window.open('about:blank', '_blank')
  expect(returned, 'the browser-boundary handle is the one transferred to the owner').toBe(handle)
  expect(open).toHaveBeenCalledTimes(1)
  expect(open).toHaveBeenCalledWith('about:blank', '_blank')
  return { handle, close, focus }
}

async function registerChild(scope: ScopeCase) {
  const lifecycle = await import('./panelPopoutLifecycle')
  const presence = await import('./panelTabPresence')
  const stopOwner = lifecycle.startPanelPopoutLifecycleOwner()
  stopOwners.push(stopOwner)
  const child = openBoundaryChild()
  const onClosed = vi.fn()
  lifecycle.registerPanelPopout({
    popoutId: scope.popoutId,
    identity: scope.identity,
    context: scope.context,
    handle: child.handle,
    onClosed,
  })
  discardChildren.push(() => lifecycle.discardPanelPopout(scope.identity, child.handle))

  // Positive controls prevent a false RED/green caused by an unregistered child
  // or a test that never exercised production handle resolution.
  expect([...presence.getPanelTabHandleRegistry().entries()], 'real registration attaches exactly this child')
    .toEqual([[presence.panelIdentityKey(scope.identity), child.handle]])
  expect(presence.resolveExistingPanelTab(scope.identity), '§8.3: a known live handle is focusable').toBe('focused')
  expect(child.focus).toHaveBeenCalledTimes(1)
  expect(child.focus).toHaveBeenCalledWith()
  expect(child.close).toHaveBeenCalledTimes(0)
  expect(child.handle.closed).toBe(false)
  expect(onClosed).toHaveBeenCalledTimes(0)
  return { lifecycle, presence, stopOwner, child, onClosed }
}

describe.each(scopeCases)('$label source reload lifecycle', (scope) => {
  it('source pagehide releases its handle without closing the registered child', async () => {
    const { presence, child, onClosed } = await registerChild(scope)

    // Dispatch on the actual source window. Never call closeAllOwned directly
    // or replace the production listener/infrastructure with a test callback.
    window.dispatchEvent(new Event('pagehide'))

    expect(child.close, '§8.3: source pagehide must lose ownership, not close the child').toHaveBeenCalledTimes(0)
    expect(child.handle.closed, 'the child must remain available for presence fallback').toBe(false)
    expect([...presence.getPanelTabHandleRegistry().entries()], 'source reload loses its old handle').toEqual([])
    expect(onClosed, 'source departure is not a child-close/re-dock event').toHaveBeenCalledTimes(0)
  })

  it('last source-owner cleanup releases its handle without closing the registered child', async () => {
    const { presence, stopOwner, child, onClosed } = await registerChild(scope)

    stopOwner()

    expect(child.close, '§8.3: last source-owner cleanup must not destroy the child').toHaveBeenCalledTimes(0)
    expect(child.handle.closed).toBe(false)
    expect([...presence.getPanelTabHandleRegistry().entries()], 'departing source no longer owns a handle').toEqual([])
    expect(onClosed, 'source cleanup must not pretend the child was closed').toHaveBeenCalledTimes(0)
  })

  it('releasing one of two owners keeps the known child attached to the remaining owner', async () => {
    const { lifecycle, presence, stopOwner, child, onClosed } = await registerChild(scope)
    const stopRemainingOwner = lifecycle.startPanelPopoutLifecycleOwner()
    stopOwners.push(stopRemainingOwner)

    stopOwner()

    expect(child.close, 'a source owner is still active').toHaveBeenCalledTimes(0)
    expect(child.handle.closed).toBe(false)
    expect([...presence.getPanelTabHandleRegistry().entries()])
      .toEqual([[presence.panelIdentityKey(scope.identity), child.handle]])
    expect(onClosed).toHaveBeenCalledTimes(0)
  })

  it('observes an explicitly closed child once and removes its actual registration', async () => {
    const { presence, child, onClosed } = await registerChild(scope)

    // Instrument control: close changes the readonly getter to true, so a
    // bad source close cannot be hidden by an always-false closed stub.
    child.handle.close()
    expect(child.close).toHaveBeenCalledTimes(1)
    expect(child.handle.closed, 'the instrument must detect a genuinely closed boundary handle').toBe(true)
    await vi.waitFor(() => {
      expect(onClosed, 'real watchPopoutClosed must observe explicit child closure').toHaveBeenCalledTimes(1)
    })
    expect(onClosed).toHaveBeenCalledWith(scope.identity, scope.context)
    expect([...presence.getPanelTabHandleRegistry().entries()]).toEqual([])
    expect(presence.resolveExistingPanelTab(scope.identity), '§8.3: a stale closed handle is not reused').toBeNull()
    expect(child.close, 'watching a user close must not issue a second close').toHaveBeenCalledTimes(1)
  })

  it('does not mistake a truly unknown identity for the attached child', async () => {
    const { presence, child, onClosed } = await registerChild(scope)

    expect(presence.resolveExistingPanelTab(scope.unknownIdentity), '§8.1/8.3: identity scope must match').toBeNull()
    expect(presence.switchToPanelTab(scope.unknownIdentity), 'an unrelated panel identity is absent').toBe('absent')
    expect(child.focus, 'unknown identity must not focus the known child').toHaveBeenCalledTimes(1)
    expect(child.close).toHaveBeenCalledTimes(0)
    expect(child.handle.closed).toBe(false)
    expect(onClosed).toHaveBeenCalledTimes(0)
  })
})
