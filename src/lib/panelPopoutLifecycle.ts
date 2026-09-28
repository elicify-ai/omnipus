import { watchPopoutClosed } from './browserLiveHandoff'
import { onLibraryPopoutClosed, onLibraryWorkspaceChanged } from './libraryHandoff'
import {
  forgetPanelTabHandle,
  panelIdentityKey,
  registerPanelTabHandle,
  type PanelIdentity,
} from './panelTabPresence'

type OwnedPanelPopout = { // not-wire-format: in-memory app-tab ownership record; never serialized or sent to the gateway
  identity: PanelIdentity
  handle: Window
  scopeUpdated: boolean
  stopWatching: () => void
  onClosed: (identity: PanelIdentity) => void
}

export type PanelPopoutRegistration = { // not-wire-format: in-memory lifecycle registration from panel content to the stable app owner
  identity: PanelIdentity
  handle: Window
  onClosed: (identity: PanelIdentity) => void
}

const ownedPopouts = new Map<string, OwnedPanelPopout>()
let ownerConsumers = 0
let stopLibraryClosed: (() => void) | null = null
let stopLibraryWorkspace: (() => void) | null = null
let pagehideListening = false

function latestOwnedPanel(panelId: PanelIdentity['panelId']): OwnedPanelPopout | undefined {
  return [...ownedPopouts.values()].reverse().find((entry) => entry.identity.panelId === panelId)
}

function findOwned(identity: PanelIdentity, handle: Window): OwnedPanelPopout | undefined {
  const direct = ownedPopouts.get(panelIdentityKey(identity))
  if (direct?.handle === handle) return direct
  return [...ownedPopouts.values()].find((entry) => entry.handle === handle)
}

function isOwned(entry: OwnedPanelPopout): boolean {
  return [...ownedPopouts.values()].includes(entry)
}

function removeOwned(entry: OwnedPanelPopout): void {
  const key = panelIdentityKey(entry.identity)
  if (ownedPopouts.get(key) === entry) ownedPopouts.delete(key)
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
  if (ownedPopouts.get(previousKey) === entry) ownedPopouts.delete(previousKey)
  forgetPanelTabHandle(entry.identity, entry.handle)
  entry.identity = identity
  entry.scopeUpdated = true
  ownedPopouts.set(nextKey, entry)
  registerPanelTabHandle(identity, entry.handle)
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
    stopLibraryWorkspace = onLibraryWorkspaceChanged((workspaceId) => {
      const entry = latestOwnedPanel('library')
      if (!entry) return
      moveOwned(entry, { panelId: 'library', workspaceId })
    })
  }
  if (stopLibraryClosed === null) {
    stopLibraryClosed = onLibraryPopoutClosed((workspaceId) => {
      const entry = latestOwnedPanel('library')
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
  const key = panelIdentityKey(registration.identity)
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
