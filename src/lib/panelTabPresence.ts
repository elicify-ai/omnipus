export type PanelIdentity = {
  panelId: string
  workspaceId?: string
  sessionId?: string
  agentId?: string
}

type PanelWindowHandle = Pick<Window, 'closed' | 'focus'>

type MutableHandleRegistry = ReadonlyMap<string, PanelWindowHandle> & {
  delete?: (key: string) => boolean
  set?: (key: string, handle: PanelWindowHandle) => unknown
}

type PanelOpenOutcome =
  | { kind: 'focused' }
  | { kind: 'affordance' }
  | { kind: 'opened' }
  | { kind: 'blocked' }

const WORKSPACE_SCOPED_PANELS = new Set([
  'library',
  'mail',
  'tasks',
  'team',
  'calendar',
])

const PRESENCE_CHANNEL_NAME = 'omnipus-panel-tab-presence'
const PRESENCE_HEARTBEAT_MS = 1_000
const PRESENCE_STALE_MS = 3_500

type PanelPresenceMessage = // not-wire-format: same-origin browser-tab lifecycle signal; never crosses the gateway or persists
  | { type: 'presence'; tabId: string; identity: PanelIdentity; sentAt: number }
  | { type: 'leave'; tabId: string }
  | { type: 'request' }
  | { type: 'focus'; tabId: string }

type PresenceEntry = {
  identity: PanelIdentity
  seenAt: number
}

export type PanelPresenceAnnouncement = {
  update: (identity: PanelIdentity) => void
  stop: () => void
}

const panelTabHandles = new Map<string, Window>()
const presenceByTab = new Map<string, PresenceEntry>()
const presenceSubscribers = new Set<() => void>()
let monitorChannel: BroadcastChannel | null = null
let monitorConsumers = 0
let monitorSweep: ReturnType<typeof setInterval> | null = null

function createTabId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function openPresenceChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') return null
  try {
    return new BroadcastChannel(PRESENCE_CHANNEL_NAME)
  } catch {
    return null
  }
}

function isPresenceMessage(value: unknown): value is PanelPresenceMessage {
  if (!isRecord(value) || typeof value.type !== 'string') return false
  if (value.type === 'request') return Object.keys(value).length === 1
  if (value.type === 'leave' || value.type === 'focus') {
    return Object.keys(value).length === 2 && isNonEmptyString(value.tabId)
  }
  return (
    value.type === 'presence' &&
    Object.keys(value).length === 4 &&
    isNonEmptyString(value.tabId) &&
    typeof value.sentAt === 'number' &&
    Number.isFinite(value.sentAt) &&
    acceptPresence(value.identity)
  )
}

function notifyPresenceSubscribers(): void {
  for (const subscriber of presenceSubscribers) subscriber()
}

function removeStalePresence(now = Date.now()): void {
  let changed = false
  for (const [tabId, entry] of presenceByTab) {
    if (now - entry.seenAt <= PRESENCE_STALE_MS) continue
    presenceByTab.delete(tabId)
    changed = true
  }
  if (changed) notifyPresenceSubscribers()
}

function handleMonitorMessage(event: MessageEvent<unknown>): void {
  const message = event.data
  if (!isPresenceMessage(message)) return
  if (message.type === 'presence') {
    const previous = presenceByTab.get(message.tabId)
    if (previous) {
      const previousKey = panelIdentityKey(previous.identity)
      const nextKey = panelIdentityKey(message.identity)
      const handle = panelTabHandles.get(previousKey)
      if (handle && previousKey !== nextKey) {
        panelTabHandles.delete(previousKey)
        if (!handle.closed) panelTabHandles.set(nextKey, handle)
      }
    }
    presenceByTab.set(message.tabId, {
      identity: message.identity,
      seenAt: Date.now(),
    })
    notifyPresenceSubscribers()
    return
  }
  if (message.type === 'leave') {
    const entry = presenceByTab.get(message.tabId)
    if (entry) panelTabHandles.delete(panelIdentityKey(entry.identity))
    if (presenceByTab.delete(message.tabId)) notifyPresenceSubscribers()
  }
}

function ensurePresenceMonitor(): void {
  if (monitorChannel) return
  monitorChannel = openPresenceChannel()
  if (!monitorChannel) return
  monitorChannel.addEventListener('message', handleMonitorMessage)
  monitorSweep = setInterval(removeStalePresence, PRESENCE_HEARTBEAT_MS)
  monitorChannel.postMessage({ type: 'request' } satisfies PanelPresenceMessage)
}

function stopPresenceMonitor(): void {
  if (monitorSweep) clearInterval(monitorSweep)
  monitorSweep = null
  if (monitorChannel) {
    monitorChannel.removeEventListener('message', handleMonitorMessage)
    monitorChannel.close()
  }
  monitorChannel = null
  if (presenceByTab.size > 0) {
    presenceByTab.clear()
    notifyPresenceSubscribers()
  }
}

/** Keep the synchronous presence list live while an app shell is mounted. */
export function startPanelTabPresenceMonitor(onChange?: () => void): () => void {
  monitorConsumers += 1
  if (onChange) presenceSubscribers.add(onChange)
  ensurePresenceMonitor()
  let stopped = false
  return () => {
    if (stopped) return
    stopped = true
    if (onChange) presenceSubscribers.delete(onChange)
    monitorConsumers -= 1
    if (monitorConsumers === 0) stopPresenceMonitor()
  }
}

/** Announce one full-page panel until its route unmounts or the page leaves. */
export function announcePanelTabPresence(initialIdentity: PanelIdentity): PanelPresenceAnnouncement {
  const channel = openPresenceChannel()
  if (!channel) return { update: () => {}, stop: () => {} }

  const tabId = createTabId()
  let identity = initialIdentity
  let stopped = false
  const publish = () => {
    channel.postMessage({ type: 'presence', tabId, identity, sentAt: Date.now() } satisfies PanelPresenceMessage)
  }
  const onMessage = (event: MessageEvent<unknown>) => {
    const message = event.data
    if (!isPresenceMessage(message)) return
    if (message.type === 'request') publish()
    if (message.type === 'focus' && message.tabId === tabId) {
      try {
        window.focus()
      } catch {
        // Browser focus is explicitly best-effort; the visible affordance is
        // the reliable part of the contract.
      }
    }
  }
  const stop = () => {
    if (stopped) return
    stopped = true
    clearInterval(heartbeat)
    window.removeEventListener('pagehide', stop)
    channel.removeEventListener('message', onMessage)
    channel.postMessage({ type: 'leave', tabId } satisfies PanelPresenceMessage)
    channel.close()
  }
  const heartbeat = setInterval(publish, PRESENCE_HEARTBEAT_MS)
  channel.addEventListener('message', onMessage)
  window.addEventListener('pagehide', stop)
  publish()

  return {
    update(nextIdentity) {
      if (stopped || !acceptPresence(nextIdentity)) return
      identity = nextIdentity
      publish()
    },
    stop,
  }
}

export function getPanelTabPresence(): PanelIdentity[] {
  removeStalePresence()
  return [...presenceByTab.values()].map((entry) => entry.identity)
}

export function getPanelTabHandleRegistry(): Map<string, Window> {
  return panelTabHandles
}

export function registerPanelTabHandle(identity: PanelIdentity, handle: Window): void {
  panelTabHandles.set(panelIdentityKey(identity), handle)
}

export function forgetPanelTabHandle(identity: PanelIdentity, handle?: Window): void {
  const key = panelIdentityKey(identity)
  if (handle && panelTabHandles.get(key) !== handle) return
  panelTabHandles.delete(key)
}

/** Focus an app-owned handle, or ask the newest matching manual tab to focus itself. */
export function focusPanelTab(identity: PanelIdentity): boolean {
  const key = panelIdentityKey(identity)
  const handle = panelTabHandles.get(key)
  if (handle) {
    if (handle.closed) {
      panelTabHandles.delete(key)
    } else {
      try {
        handle.focus()
      } catch {
        // Focus is best-effort; do not create a duplicate on failure.
      }
      return true
    }
  }

  removeStalePresence()
  const match = [...presenceByTab.entries()]
    .filter(([, entry]) => panelIdentityKey(entry.identity) === key)
    .sort((a, b) => b[1].seenAt - a[1].seenAt)[0]
  if (!match || !monitorChannel) return false
  monitorChannel.postMessage({ type: 'focus', tabId: match[0] } satisfies PanelPresenceMessage)
  return true
}

export function resolveExistingPanelTab(identity: PanelIdentity): 'focused' | 'affordance' | null {
  const key = panelIdentityKey(identity)
  const handle = panelTabHandles.get(key)
  if (handle) {
    if (handle.closed) {
      panelTabHandles.delete(key)
    } else {
      focusPanelTab(identity)
      return 'focused'
    }
  }
  return getPanelTabPresence().some((entry) => panelIdentityKey(entry) === key) ? 'affordance' : null
}

export function panelIdentityKey(identity: PanelIdentity): string {
  if (identity.panelId === 'browser') {
    return `browser:${identity.sessionId ?? ''}:${identity.agentId ?? ''}`
  }
  return `${identity.panelId}:${identity.workspaceId ?? 'app'}`
}

export function resolvePanelOpen(input: {
  identity: PanelIdentity
  handles: ReadonlyMap<string, PanelWindowHandle>
  presence: PanelIdentity[]
  open: () => Window | null
}): PanelOpenOutcome {
  const key = panelIdentityKey(input.identity)
  const registry = input.handles as MutableHandleRegistry
  const existing = input.handles.get(key)

  if (existing) {
    if (!existing.closed) {
      try {
        existing.focus()
      } catch {
        // Window focus is best-effort. Do not create a duplicate merely
        // because the browser declined to bring the existing tab forward.
      }
      return { kind: 'focused' }
    }
    registry.delete?.(key)
  }

  if (input.presence.some((identity) => panelIdentityKey(identity) === key)) {
    return { kind: 'affordance' }
  }

  let opened: Window | null
  try {
    opened = input.open()
  } catch {
    return { kind: 'blocked' }
  }
  if (!opened || opened.closed) return { kind: 'blocked' }

  registry.set?.(key, opened)
  return { kind: 'opened' }
}

export function acceptPresence(message: unknown): boolean {
  if (!isRecord(message)) return false

  const keys = Object.keys(message)
  if (keys.some((key) => !['panelId', 'workspaceId', 'sessionId', 'agentId'].includes(key))) {
    return false
  }

  const panelId = message.panelId
  if (typeof panelId !== 'string') return false

  if (panelId === 'browser') {
    return (
      keys.length === 3 &&
      isNonEmptyString(message.sessionId) &&
      isNonEmptyString(message.agentId)
    )
  }

  if (!WORKSPACE_SCOPED_PANELS.has(panelId)) return false
  if ('sessionId' in message || 'agentId' in message) return false
  return message.workspaceId === undefined || isNonEmptyString(message.workspaceId)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0
}
