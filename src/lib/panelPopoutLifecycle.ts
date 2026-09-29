import { watchPopoutClosed } from './browserLiveHandoff'
import { isWorkspaceScopedPanel } from '@/components/panel-shell/types'
import type { PanelContext, PanelId, WorkspacePanelContext } from '@/components/panel-shell/types'
import {
  forgetPanelTabHandle,
  panelIdentityFromContext,
  panelIdentityKey,
  registerPanelTabHandle,
  type PanelIdentity,
} from './panelTabPresence'

const CHANNEL_NAME = 'omnipus-panel-popout-lifecycle'

type PanelPopoutMessage = // not-wire-format: same-origin browser lifecycle signal
  | { type: 'context-changed'; panelId: PanelId; popoutId: string; context: PanelContext }
  | { type: 'popout-closed'; panelId: PanelId; popoutId: string; context: PanelContext }

type OwnedPanelPopout = { // not-wire-format: in-memory app-tab ownership record
  ownershipKey: string
  popoutId: string
  identity: PanelIdentity
  context: PanelContext
  handle: Window
  stopWatching: () => void
  onClosed: (identity: PanelIdentity, context: PanelContext) => void
}

export type PanelPopoutRegistration = { // not-wire-format: in-memory lifecycle registration
  popoutId: string
  identity: PanelIdentity
  context: PanelContext
  handle: Window
  onClosed: (identity: PanelIdentity, context: PanelContext) => void
}

const ownedPopouts = new Map<string, OwnedPanelPopout>()
let ownerConsumers = 0
let lifecycleChannel: BroadcastChannel | null = null
let pagehideListening = false

function openChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') return null
  try {
    return new BroadcastChannel(CHANNEL_NAME)
  } catch {
    return null
  }
}

function ownershipKey(registration: PanelPopoutRegistration): string {
  return `popout:${registration.popoutId}`
}

function findOwnedPanelPopout(panelId: PanelId, popoutId: string): OwnedPanelPopout | undefined {
  const entry = ownedPopouts.get(`popout:${popoutId}`)
  return entry?.identity.panelId === panelId ? entry : undefined
}

function findOwned(identity: PanelIdentity, handle: Window): OwnedPanelPopout | undefined {
  const identityKey = panelIdentityKey(identity)
  return [...ownedPopouts.values()].find(
    (entry) => entry.handle === handle && panelIdentityKey(entry.identity) === identityKey,
  )
}

function isOwned(entry: OwnedPanelPopout): boolean {
  return [...ownedPopouts.values()].includes(entry)
}

function removeOwned(entry: OwnedPanelPopout): void {
  if (ownedPopouts.get(entry.ownershipKey) === entry) ownedPopouts.delete(entry.ownershipKey)
  entry.stopWatching()
  forgetPanelTabHandle(entry.identity, entry.handle)
}

function finishOwned(entry: OwnedPanelPopout, finalContext = entry.context): void {
  if (!isOwned(entry)) return
  const finalIdentity = panelIdentityFromContext(entry.identity.panelId, finalContext) ?? entry.identity
  removeOwned(entry)
  entry.onClosed(finalIdentity, finalContext)
  maybeStopInfrastructure()
}

function moveOwned(entry: OwnedPanelPopout, context: PanelContext): void {
  const nextIdentity = panelIdentityFromContext(entry.identity.panelId, context)
  if (!nextIdentity) return
  entry.context = context
  if (panelIdentityKey(entry.identity) === panelIdentityKey(nextIdentity)) return
  forgetPanelTabHandle(entry.identity, entry.handle)
  entry.identity = nextIdentity
  registerPanelTabHandle(nextIdentity, entry.handle)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isPanelId(value: unknown): value is PanelId {
  return typeof value === 'string' && ['library', 'browser', 'mail', 'tasks', 'team', 'calendar'].includes(value)
}

function isContextForPanel(panelId: PanelId, value: unknown): value is PanelContext {
  if (!isRecord(value)) return false
  if (panelId === 'browser') {
    return typeof value.sessionId === 'string' && value.sessionId.length > 0 &&
      typeof value.agentId === 'string' && value.agentId.length > 0
  }
  if ('sessionId' in value || 'agentId' in value) return false
  const optionalNonEmptyString = (candidate: unknown) =>
    candidate === undefined || (typeof candidate === 'string' && candidate.length > 0)
  const optionalNullableNonEmptyString = (candidate: unknown) =>
    candidate === undefined || candidate === null ||
    (typeof candidate === 'string' && candidate.length > 0)
  const optionalOpaqueString = (candidate: unknown) =>
    candidate === undefined || candidate === null || typeof candidate === 'string'
  return optionalNonEmptyString(value.workspaceId) &&
    optionalNullableNonEmptyString(value.mailboxId) &&
    optionalNonEmptyString(value.path) &&
    (value.folder === undefined || value.folder === null ||
      value.folder === 'inbox' || value.folder === 'sent' || value.folder === 'drafts') &&
    optionalOpaqueString(value.messageRef)
}

function acceptMessage(value: unknown): value is PanelPopoutMessage {
  if (!isRecord(value) || (value.type !== 'context-changed' && value.type !== 'popout-closed')) return false
  return isPanelId(value.panelId) && typeof value.popoutId === 'string' && value.popoutId.length > 0 &&
    isContextForPanel(value.panelId, value.context)
}

function handleMessage(event: MessageEvent<unknown>): void {
  const message = event.data
  if (!acceptMessage(message)) return
  const entry = findOwnedPanelPopout(message.panelId, message.popoutId)
  if (!entry) return
  if (message.type === 'context-changed') moveOwned(entry, message.context)
  else finishOwned(entry, message.context)
}

/** Tell the opener which address the full-screen panel currently shows. */
export function announcePanelPopoutContext(
  panelId: PanelId,
  popoutId: string,
  context: PanelContext,
): void {
  postMessage({ type: 'context-changed', panelId, popoutId, context })
}

/** Tell the opener that the full-screen panel is leaving. */
export function announcePanelPopoutClosed(
  panelId: PanelId,
  popoutId: string,
  context: PanelContext,
): void {
  postMessage({ type: 'popout-closed', panelId, popoutId, context })
}

function postMessage(message: PanelPopoutMessage): void {
  const channel = openChannel()
  if (!channel) return
  try {
    channel.postMessage(message)
  } finally {
    channel.close()
  }
}

/** Compatibility seam for workspace-scoped panels; the generic route sends full contexts. */
export function updatePanelPopoutWorkspace(
  panelId: PanelId,
  popoutId: string,
  workspaceId?: string,
): void {
  const entry = findOwnedPanelPopout(panelId, popoutId)
  if (!entry || !isWorkspaceScopedPanel(panelId)) return
  moveOwned(entry, { ...(entry.context as WorkspacePanelContext), workspaceId })
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
  if (lifecycleChannel === null) {
    lifecycleChannel = openChannel()
    lifecycleChannel?.addEventListener('message', handleMessage)
  }
  if (!pagehideListening) {
    window.addEventListener('pagehide', closeAllOwned)
    pagehideListening = true
  }
}

function maybeStopInfrastructure(): void {
  if (ownerConsumers > 0 || ownedPopouts.size > 0) return
  lifecycleChannel?.removeEventListener('message', handleMessage)
  lifecycleChannel?.close()
  lifecycleChannel = null
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

/** Transfer a newly opened child from the side-panel shell to the stable app owner. */
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
    stopWatching: () => {},
  }
  ownedPopouts.set(key, entry)
  // Polling reads this entry's live context after every handoff update.
  entry.stopWatching = watchPopoutClosed(entry.handle, () => finishOwned(entry))
}

/** Roll back a failed handover without firing the normal re-dock callback. */
export function discardPanelPopout(identity: PanelIdentity, handle: Window): void {
  const entry = findOwned(identity, handle)
  if (!entry) return
  removeOwned(entry)
  maybeStopInfrastructure()
}
