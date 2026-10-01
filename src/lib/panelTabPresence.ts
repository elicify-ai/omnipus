import { PANEL_POLICIES, isWorkspaceScopedPanel } from '@/components/panel-shell/types'
import type { PanelContext, PanelId, WorkspacePanelId } from '@/components/panel-shell/types'

export type PanelIdentity = // not-wire-format: SPA-local identity; only its SHA-256 key is shared between tabs, never sent to the gateway
  | { panelId: 'browser'; sessionId: string; agentId: string; workspaceId?: never }
  | { panelId: WorkspacePanelId; workspaceId?: string; sessionId?: never; agentId?: never }

/** Derive the exclusive tab identity from a registered panel's render context. */
export function panelIdentityFromContext(panelId: PanelId, context: PanelContext): PanelIdentity | null {
  if (panelId === 'browser') {
    const { sessionId, agentId } = context
    return sessionId && agentId ? { panelId, sessionId, agentId } : null
  }
  return { panelId, workspaceId: context.workspaceId }
}

type PanelWindowHandle = Pick<Window, 'closed' | 'focus'>

type MutableHandleRegistry = ReadonlyMap<string, PanelWindowHandle> & // not-wire-format: in-memory adapter for browser Window handles, never serialized or sent to the gateway
  Pick<Map<string, PanelWindowHandle>, 'delete' | 'set'>

type PanelOpenOutcome =
  | { kind: 'focused' }
  | { kind: 'focus-failed' }
  | { kind: 'affordance' }
  | { kind: 'opened' }
  | { kind: 'blocked' }

export type PanelTabFocusResult = 'focused' | 'requested' | 'absent' | 'failed'

const PRESENCE_CHANNEL_NAME = 'omnipus-panel-tab-presence'
const PRESENCE_HEARTBEAT_MS = 1_000
const PRESENCE_STALE_MS = 3_500

/** Per-tab capability that prevents a replayed focus message targeting another tab. */
type FocusNonce = string

type PanelPresenceMessage = // not-wire-format: same-origin browser-tab lifecycle signal; never crosses the gateway or persists
  | { type: 'presence'; tabId: string; identityKey: string; focusNonce: FocusNonce; sentAt: number }
  | { type: 'leave'; tabId: string }
  | { type: 'request' }
  | { type: 'focus'; tabId: string; focusNonce: FocusNonce }
  | { type: 'focus-failed'; tabId: string; focusNonce: FocusNonce }

type PresenceEntry = { // not-wire-format: in-memory same-origin presence cache entry, never serialized or sent to the gateway
  identityKey: string
  focusNonce: FocusNonce
  seenAt: number
}

export type PanelPresenceAnnouncement = { // not-wire-format: SPA-local lifecycle controller returned within one tab, never serialized or sent to the gateway
  update: (identity: PanelIdentity) => void
  stop: () => void
}

const panelTabHandles = new Map<string, Window>()
const presenceByTab = new Map<string, PresenceEntry>()
const localIdentityByOpaqueKey = new Map<string, PanelIdentity>()
// Longer than PRESENCE_STALE_MS so a recent Switch can observe one stale-presence sweep.
const PANEL_FOCUS_FALLBACK_MS = 5_000
const localFocusFallbacks = new Map<string, {
  identity: PanelIdentity
  panelIntentRevision: number
  isPanelIntentCurrent: (revision: number) => boolean
  onUnavailable: () => void
  onFocusFailed: () => void
  timer: ReturnType<typeof setTimeout>
}>()
const presenceSubscribers = new Set<() => void>()
let monitorChannel: BroadcastChannel | null = null
let monitorConsumers = 0
let monitorSweep: ReturnType<typeof setInterval> | null = null
let broadcastUnavailableWarned = false
let invalidIdentityWarned = false

const SHA256_INITIAL = [
  0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
  0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
]

const SHA256_ROUND = [
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
] as const

function rotateRight(value: number, bits: number): number {
  return (value >>> bits) | (value << (32 - bits))
}

/** Synchronous SHA-256 keeps the presence check inside the user gesture. */
function sha256Hex(value: string): string {
  const input = new TextEncoder().encode(value)
  const paddedLength = Math.ceil((input.length + 9) / 64) * 64
  const bytes = new Uint8Array(paddedLength)
  bytes.set(input)
  bytes[input.length] = 0x80
  const bitLength = input.length * 8
  const view = new DataView(bytes.buffer)
  view.setUint32(paddedLength - 8, Math.floor(bitLength / 0x100000000), false)
  view.setUint32(paddedLength - 4, bitLength >>> 0, false)

  const hash = [...SHA256_INITIAL]
  const words = new Uint32Array(64)
  for (let offset = 0; offset < bytes.length; offset += 64) {
    for (let index = 0; index < 16; index += 1) {
      words[index] = view.getUint32(offset + index * 4, false)
    }
    for (let index = 16; index < 64; index += 1) {
      const first = words[index - 15]!
      const second = words[index - 2]!
      const sigma0 = rotateRight(first, 7) ^ rotateRight(first, 18) ^ (first >>> 3)
      const sigma1 = rotateRight(second, 17) ^ rotateRight(second, 19) ^ (second >>> 10)
      words[index] = (words[index - 16]! + sigma0 + words[index - 7]! + sigma1) >>> 0
    }

    let [a, b, c, d, e, f, g, h] = hash
    for (let index = 0; index < 64; index += 1) {
      const upper = rotateRight(e!, 6) ^ rotateRight(e!, 11) ^ rotateRight(e!, 25)
      const choose = (e! & f!) ^ (~e! & g!)
      const first = (h! + upper + choose + SHA256_ROUND[index]! + words[index]!) >>> 0
      const lower = rotateRight(a!, 2) ^ rotateRight(a!, 13) ^ rotateRight(a!, 22)
      const majority = (a! & b!) ^ (a! & c!) ^ (b! & c!)
      const second = (lower + majority) >>> 0
      h = g
      g = f
      f = e
      e = (d! + first) >>> 0
      d = c
      c = b
      b = a
      a = (first + second) >>> 0
    }
    hash[0] = (hash[0]! + a!) >>> 0
    hash[1] = (hash[1]! + b!) >>> 0
    hash[2] = (hash[2]! + c!) >>> 0
    hash[3] = (hash[3]! + d!) >>> 0
    hash[4] = (hash[4]! + e!) >>> 0
    hash[5] = (hash[5]! + f!) >>> 0
    hash[6] = (hash[6]! + g!) >>> 0
    hash[7] = (hash[7]! + h!) >>> 0
  }
  return hash.map((word) => word.toString(16).padStart(8, '0')).join('')
}

function createTabId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function openPresenceChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') {
    warnBroadcastUnavailable()
    return null
  }
  try {
    return new BroadcastChannel(PRESENCE_CHANNEL_NAME)
  } catch {
    warnBroadcastUnavailable()
    return null
  }
}

function warnBroadcastUnavailable(): void {
  if (broadcastUnavailableWarned) return
  broadcastUnavailableWarned = true
  console.warn('Panel tab presence is unavailable because BroadcastChannel could not be opened.')
}

function warnInvalidIdentity(): void {
  if (invalidIdentityWarned) return
  invalidIdentityWarned = true
  console.warn('Panel tab presence ignored an invalid panel identity.')
}

export function acceptPanelPresenceMessage(value: unknown): value is PanelPresenceMessage {
  if (!isRecord(value) || typeof value.type !== 'string') return false
  if (value.type === 'request') return Object.keys(value).length === 1
  if (value.type === 'leave') {
    return Object.keys(value).length === 2 && isTabId(value.tabId)
  }
  if (value.type === 'focus' || value.type === 'focus-failed') {
    return Object.keys(value).length === 3 && isTabId(value.tabId) && isFocusNonce(value.focusNonce)
  }
  return (
    value.type === 'presence' &&
    Object.keys(value).length === 5 &&
    isTabId(value.tabId) &&
    isOpaqueIdentityKey(value.identityKey) &&
    isFocusNonce(value.focusNonce) &&
    typeof value.sentAt === 'number' &&
    Number.isFinite(value.sentAt) &&
    value.sentAt >= 0
  )
}

function notifyPresenceSubscribers(): void {
  for (const subscriber of presenceSubscribers) subscriber()
  flushPanelFocusFallbacks()
}

function hasLivePanelTarget(identity: PanelIdentity): boolean {
  const key = panelIdentityKey(identity)
  const handle = panelTabHandles.get(key)
  if (handle?.closed) panelTabHandles.delete(key)
  if (handle && !handle.closed) return true
  const opaqueKey = panelPresenceKey(identity)
  return [...presenceByTab.values()].some((entry) => entry.identityKey === opaqueKey)
}

function flushPanelFocusFallbacks(): void {
  for (const [key, fallback] of localFocusFallbacks) {
    if (!fallback.isPanelIntentCurrent(fallback.panelIntentRevision)) {
      localFocusFallbacks.delete(key)
      clearTimeout(fallback.timer)
      continue
    }
    if (hasLivePanelTarget(fallback.identity)) continue
    localFocusFallbacks.delete(key)
    clearTimeout(fallback.timer)
    fallback.onUnavailable()
  }
}

function failPanelFocusFallback(identityKey: string): void {
  for (const [key, fallback] of localFocusFallbacks) {
    if (panelPresenceKey(fallback.identity) !== identityKey) continue
    localFocusFallbacks.delete(key)
    clearTimeout(fallback.timer)
    if (!fallback.isPanelIntentCurrent(fallback.panelIntentRevision)) continue
    fallback.onFocusFailed()
  }
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
  if (!acceptPanelPresenceMessage(message)) return
  if (message.type === 'presence') {
    presenceByTab.set(message.tabId, {
      identityKey: message.identityKey,
      focusNonce: message.focusNonce,
      seenAt: Date.now(),
    })
    notifyPresenceSubscribers()
    return
  }
  if (message.type === 'leave') {
    if (presenceByTab.delete(message.tabId)) notifyPresenceSubscribers()
    return
  }
  if (message.type === 'focus-failed') {
    const entry = presenceByTab.get(message.tabId)
    if (entry?.focusNonce === message.focusNonce) {
      failPanelFocusFallback(entry.identityKey)
    }
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
  for (const fallback of localFocusFallbacks.values()) clearTimeout(fallback.timer)
  localFocusFallbacks.clear()
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
  if (!acceptPresence(initialIdentity)) {
    warnInvalidIdentity()
    return { update: () => {}, stop: () => {} }
  }
  const channel = openPresenceChannel()
  if (!channel) return { update: () => {}, stop: () => {} }

  const tabId = createTabId()
  const focusNonce = createTabId()
  let identity = initialIdentity
  let identityKey = panelPresenceKey(initialIdentity)
  localIdentityByOpaqueKey.set(identityKey, initialIdentity)
  let stopped = false
  const publish = () => {
    channel.postMessage({
      type: 'presence',
      tabId,
      identityKey,
      focusNonce,
      sentAt: Date.now(),
    } satisfies PanelPresenceMessage)
  }
  const onMessage = (event: MessageEvent<unknown>) => {
    const message = event.data
    if (!acceptPanelPresenceMessage(message)) return
    if (message.type === 'request') publish()
    if (
      message.type === 'focus' &&
      message.tabId === tabId &&
      message.focusNonce === focusNonce
    ) {
      try {
        window.focus()
      } catch {
        channel.postMessage({
          type: 'focus-failed',
          tabId,
          focusNonce,
        } satisfies PanelPresenceMessage)
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
    localIdentityByOpaqueKey.delete(identityKey)
  }
  const heartbeat = setInterval(publish, PRESENCE_HEARTBEAT_MS)
  channel.addEventListener('message', onMessage)
  window.addEventListener('pagehide', stop)
  publish()

  return {
    update(nextIdentity) {
      if (stopped) return
      if (!acceptPresence(nextIdentity)) {
        warnInvalidIdentity()
        return
      }
      localIdentityByOpaqueKey.delete(identityKey)
      identity = nextIdentity
      identityKey = panelPresenceKey(nextIdentity)
      localIdentityByOpaqueKey.set(identityKey, identity)
      publish()
    },
    stop,
  }
}

export function getPanelTabPresence(): PanelIdentity[] {
  removeStalePresence()
  return [...presenceByTab.values()]
    .map((entry) => localIdentityByOpaqueKey.get(entry.identityKey))
    .filter((identity): identity is PanelIdentity => identity !== undefined)
}

export function getPanelTabPresenceKeys(): readonly string[] {
  removeStalePresence()
  return [...presenceByTab.values()].map((entry) => entry.identityKey)
}

export function getPanelTabHandleRegistry(): ReadonlyMap<string, Window> {
  return new Map(panelTabHandles)
}

export function registerPanelTabHandle(identity: PanelIdentity, handle: Window): void {
  if (!acceptPresence(identity)) throw new TypeError('Invalid panel identity')
  panelTabHandles.set(panelIdentityKey(identity), handle)
}

export function forgetPanelTabHandle(identity: PanelIdentity, handle?: Window): void {
  const key = panelIdentityKey(identity)
  if (handle && panelTabHandles.get(key) !== handle) return
  panelTabHandles.delete(key)
}

/** Focus an app-owned handle, or ask the newest matching manual tab to focus itself. */
export function switchToPanelTab(identity: PanelIdentity): PanelTabFocusResult {
  const key = panelIdentityKey(identity)
  const handle = panelTabHandles.get(key)
  if (handle) {
    if (handle.closed) {
      panelTabHandles.delete(key)
    } else {
      try {
        handle.focus()
      } catch {
        return 'failed'
      }
      return 'focused'
    }
  }

  removeStalePresence()
  const opaqueKey = panelPresenceKey(identity)
  const match = [...presenceByTab.entries()]
    .filter(([, entry]) => entry.identityKey === opaqueKey)
    .sort((a, b) => b[1].seenAt - a[1].seenAt)[0]
  if (!match || !monitorChannel) return 'absent'
  monitorChannel.postMessage({
    type: 'focus',
    tabId: match[0],
    focusNonce: match[1].focusNonce,
  } satisfies PanelPresenceMessage)
  return 'requested'
}

/** Boolean form for callers that only need best-effort focus. */
export function focusPanelTab(identity: PanelIdentity): boolean {
  const result = switchToPanelTab(identity)
  return result === 'focused' || result === 'requested'
}

export function resolveExistingPanelTab(
  identity: PanelIdentity,
): 'focused' | 'focus-failed' | 'affordance' | null {
  const key = panelIdentityKey(identity)
  const handle = panelTabHandles.get(key)
  if (handle) {
    if (handle.closed) {
      panelTabHandles.delete(key)
    } else {
      const result = switchToPanelTab(identity)
      return result === 'focused' ? 'focused' : 'focus-failed'
    }
  }
  return getPanelTabPresenceKeys().includes(panelPresenceKey(identity)) ? 'affordance' : null
}

export function panelIdentityKey(identity: PanelIdentity): string {
  if (identity.panelId === 'browser') {
    return `browser:${identity.sessionId}:${identity.agentId}`
  }
  return `${identity.panelId}:${identity.workspaceId ?? 'app'}`
}

export function panelPresenceKey(identity: PanelIdentity): string {
  return sha256Hex(panelIdentityKey(identity))
}

export function armPanelFocusFallback(
  identity: PanelIdentity,
  panelIntentRevision: number,
  isPanelIntentCurrent: (revision: number) => boolean,
  onUnavailable: () => void,
  onFocusFailed: () => void,
): void {
  const key = panelIdentityKey(identity)
  cancelPanelFocusFallback(identity)
  const fallback = {
    identity,
    panelIntentRevision,
    isPanelIntentCurrent,
    onUnavailable,
    onFocusFailed,
    timer: setTimeout(() => {
      if (localFocusFallbacks.get(key) === fallback) localFocusFallbacks.delete(key)
    }, PANEL_FOCUS_FALLBACK_MS),
  }
  localFocusFallbacks.set(key, fallback)
}

export function cancelPanelFocusFallback(identity: PanelIdentity): void {
  const fallback = localFocusFallbacks.get(panelIdentityKey(identity))
  if (!fallback) return
  clearTimeout(fallback.timer)
  localFocusFallbacks.delete(panelIdentityKey(identity))
}

export function resolvePanelOpen(input: {
  identity: PanelIdentity
  handles: MutableHandleRegistry
  presence: PanelIdentity[]
  open: () => Window | null
}): PanelOpenOutcome {
  const key = panelIdentityKey(input.identity)
  const registry = input.handles
  const existing = input.handles.get(key)

  if (existing) {
    if (!existing.closed) {
      try {
        existing.focus()
      } catch {
        return { kind: 'focus-failed' }
      }
      return { kind: 'focused' }
    }
    registry.delete(key)
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

  registry.set(key, opened)
  return { kind: 'opened' }
}

export function resolveRegisteredPanelOpen(input: {
  identity: PanelIdentity
  open: () => Window | null
}): PanelOpenOutcome {
  const presenceKey = panelPresenceKey(input.identity)
  const present = getPanelTabPresenceKeys().includes(presenceKey)
  return resolvePanelOpen({
    identity: input.identity,
    handles: panelTabHandles,
    presence: present ? [input.identity] : [],
    open: input.open,
  })
}

export function acceptPresence(message: unknown): message is PanelIdentity {
  if (!isRecord(message)) return false

  const keys = Object.keys(message)
  if (keys.some((key) => !['panelId', 'workspaceId', 'sessionId', 'agentId'].includes(key))) {
    return false
  }

  const panelId = message.panelId
  if (!isPanelId(panelId)) return false

  if (panelId === 'browser') {
    return (
      keys.length === 3 &&
      isNonEmptyString(message.sessionId) &&
      isNonEmptyString(message.agentId)
    )
  }

  if (!isWorkspaceScopedPanel(panelId)) return false
  if ('sessionId' in message || 'agentId' in message) return false
  return message.workspaceId === undefined || isNonEmptyString(message.workspaceId)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0
}

function isPanelId(value: unknown): value is PanelId {
  return typeof value === 'string' && value in PANEL_POLICIES
}

function isOpaqueIdentityKey(value: unknown): value is string {
  return typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)
}

function isTabId(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9-]{8,128}$/.test(value)
}

function isFocusNonce(value: unknown): value is string {
  return isTabId(value)
}
