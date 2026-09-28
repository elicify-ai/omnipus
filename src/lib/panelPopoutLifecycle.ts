import { watchPopoutClosed } from './browserLiveHandoff'
import {
  LEGACY_LIBRARY_HANDOFF_ID,
  onLibraryPopoutClosed,
  onLibraryWorkspaceChanged,
} from './libraryHandoff'
import { isWorkspaceScopedPanel } from '@/components/panel-shell/types'
import type { PanelId } from '@/components/panel-shell/types'
import {
  forgetPanelTabHandle,
  panelIdentityKey,
  registerPanelTabHandle,
  type PanelIdentity,
} from './panelTabPresence'

type OwnedPanelPopout = { // not-wire-format: in-memory app-tab ownership record; never serialized or sent to the gateway
  ownershipKey: string
  popoutId?: string
  identity: PanelIdentity
  handle: Window
  scopeUpdated: boolean
  stopWatching: () => void
  onClosed: (identity: PanelIdentity) => void
}

export type PanelPopoutRegistration = { // not-wire-format: in-memory lifecycle registration from panel content to the stable app owner
  popoutId?: string
  identity: PanelIdentity
  handle: Window
  onClosed: (identity: PanelIdentity) => void
}

const ownedPopouts = new Map<string, OwnedPanelPopout>()
let ownerConsumers = 0
let stopLibraryClosed: (() => void) | null = null
let stopLibraryWorkspace: (() => void) | null = null
let pagehideListening = false

function ownershipKey(registration: PanelPopoutRegistration): string {
  return registration.popoutId
    ? `popout:${registration.popoutId}`
    : `identity:${panelIdentityKey(registration.identity)}`
}

function findOwnedPanelPopout(panelId: PanelId, popoutId: string): OwnedPanelPopout | undefined {
  const direct = ownedPopouts.get(`popout:${popoutId}`)
  if (direct?.identity.panelId === panelId) return direct
  if (panelId !== 'library' || popoutId !== LEGACY_LIBRARY_HANDOFF_ID) return undefined
  // Old Library children predate pop-out ids. Falling back is unambiguous only
  // while exactly one Library child is owned; otherwise no signal is routed.
  const libraries = [...ownedPopouts.values()].filter((entry) => entry.identity.panelId === 'library')
  return libraries.length === 1 ? libraries[0] : undefined
}

function findOwned(identity: PanelIdentity, handle: Window): OwnedPanelPopout | undefined {
  const direct = ownedPopouts.get(`identity:${panelIdentityKey(identity)}`)
  if (direct?.handle === handle) return direct
  return [...ownedPopouts.values()].find((entry) => entry.handle === handle)
}

function isOwned(entry: OwnedPanelPopout): boolean {
  return [...ownedPopouts.values()].includes(entry)
}

function removeOwned(entry: OwnedPanelPopout): void {
  if (ownedPopouts.get(entry.ownershipKey) === entry) ownedPopouts.delete(entry.ownershipKey)
  entry.stopWatching()
  forgetPanelTabHandle(entry.identity, entry.handle)
}

function finishOwned(entry: OwnedPanelPopout, fallbackIdentity?: PanelIdentity): void {
  if (!isOwned(entry)) return
  const finalIdentity = entry.scopeUpdated || fallbackIdentity === undefined
    ? entry.identity
    : fallbackIdentity
  removeOwned(entry)
  entry.onClosed(finalIdentity)
  maybeStopInfrastructure()
}

function moveOwned(entry: OwnedPanelPopout, identity: PanelIdentity): void {
  const previousKey = panelIdentityKey(entry.identity)
  const nextKey = panelIdentityKey(identity)
  if (previousKey === nextKey) {
    entry.scopeUpdated = true
    return
  }
  forgetPanelTabHandle(entry.identity, entry.handle)
  entry.identity = identity
  entry.scopeUpdated = true
  registerPanelTabHandle(identity, entry.handle)
}

/** Re-key a pop-out to what the child currently shows; outer switch policy does not apply here. */
export function updatePanelPopoutWorkspace(
  panelId: PanelId,
  popoutId: string,
  workspaceId?: string,
): void {
  const entry = findOwnedPanelPopout(panelId, popoutId)
  if (!entry || !isWorkspaceScopedPanel(panelId)) return
  moveOwned(entry, { panelId, workspaceId })
}

function closeAllOwned(): void {
  const entries = [...ownedPopouts.values()]
  ownedPopouts.clear()
  for (const entry of entries) {
    entry.stopWatching()
    forgetPanelTabHandle(entry.identity, entry.handle)
    try {
      entry.handle.close()
    } catch {
      // Page teardown is already in progress; the browser owns final cleanup.
    }
  }
}

function ensureInfrastructure(): void {
  if (stopLibraryWorkspace === null) {
    stopLibraryWorkspace = onLibraryWorkspaceChanged((popoutId, workspaceId) => {
      updatePanelPopoutWorkspace('library', popoutId, workspaceId)
    })
  }
  if (stopLibraryClosed === null) {
    stopLibraryClosed = onLibraryPopoutClosed((popoutId, workspaceId) => {
      const entry = findOwnedPanelPopout('library', popoutId)
      if (!entry) return
      finishOwned(entry, { panelId: 'library', workspaceId })
    })
  }
  if (!pagehideListening) {
    window.addEventListener('pagehide', closeAllOwned)
    pagehideListening = true
  }
}

function maybeStopInfrastructure(): void {
  if (ownerConsumers > 0 || ownedPopouts.size > 0) return
  stopLibraryWorkspace?.()
  stopLibraryWorkspace = null
  stopLibraryClosed?.()
  stopLibraryClosed = null
  if (pagehideListening) window.removeEventListener('pagehide', closeAllOwned)
  pagehideListening = false
}

/** Keep pop-out ownership alive for the lifetime of the application shell. */
export function startPanelPopoutLifecycleOwner(): () => void {
  ownerConsumers += 1
  ensureInfrastructure()
  let stopped = false
  return () => {
    if (stopped) return
    stopped = true
    ownerConsumers -= 1
    if (ownerConsumers === 0) closeAllOwned()
    maybeStopInfrastructure()
  }
}

/** Transfer a newly opened child from panel content to the stable app owner. */
export function registerPanelPopout(registration: PanelPopoutRegistration): void {
  ensureInfrastructure()
  registerPanelTabHandle(registration.identity, registration.handle)
  const key = ownershipKey(registration)
  const previous = ownedPopouts.get(key)
  if (previous) {
    removeOwned(previous)
    if (previous.handle !== registration.handle) {
      try {
        previous.handle.close()
      } catch {
        // Replacing a stale child is best-effort; the new record remains valid.
      }
    }
  }
  const entry: OwnedPanelPopout = {
    ...registration,
    ownershipKey: key,
    scopeUpdated: false,
    stopWatching: () => {},
  }
  ownedPopouts.set(key, entry)
  entry.stopWatching = watchPopoutClosed(entry.handle, () => finishOwned(entry))
}

/** Roll back a failed handover without firing the normal re-dock callback. */
export function discardPanelPopout(identity: PanelIdentity, handle: Window): void {
  const entry = findOwned(identity, handle)
  if (!entry) return
  removeOwned(entry)
  maybeStopInfrastructure()
}

/**
 * Isolated component tests have no app owner. In that environment only,
 * preserve the legacy cleanup contract when the content itself unmounts.
 */
export function releasePanelPopoutWithoutAppOwner(identity: PanelIdentity, handle: Window): void {
  if (ownerConsumers > 0) return
  discardPanelPopout(identity, handle)
  try {
    handle.close()
  } catch {
    // Best-effort cleanup for an isolated mount.
  }
}
