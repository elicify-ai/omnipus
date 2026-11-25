// permissionSaveQueue — the app-level save queue of Skills & Tools
// (spec C-16, Dataset D12).
//
// One queue per app (per QueryClient), so it outlives the page: a save that
// was started keeps going when the person leaves, a reopened page joins the
// same queue, and a late outcome is reported with one toast.
//
// Rules (C-16):
//   - a click shows "Saving…" at once and is sent within 1000 ms;
//   - at most one PATCH is in flight; changes made meanwhile wait, and a later
//     value of the same key replaces the waiting one;
//   - every PATCH names only the changed keys and is sent with keepalive;
//   - after a refusal, a 500 or an unknown outcome, a fresh GET re-reads what
//     is stored before the next PATCH goes out;
//   - a 503 means stored but not applied: the value stays, with Retry.

import { useEffect, useSyncExternalStore } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import type {
  PermissionSettings,
  PermissionSettingsPatch,
  ToolPolicy,
  ModelVisibility,
} from '@/lib/api/generated/openapi-types'
import { fetchPermissionSettings, patchPermissionSettings, serverErrorText } from '@/lib/api/permissions'
import { isApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'

export type PermissionSection = 'policies' | 'skill_policies' | 'visibility' | 'skill_visibility' | 'skill_trust'
type MapSection = Exclude<PermissionSection, 'skill_trust'>
type SettingValue = ToolPolicy | ModelVisibility

/** The status a row shows for its own last change. // not-wire-format */
export type SaveStatus =
  | { kind: 'saving' }
  | { kind: 'saved' }
  | { kind: 'refused'; text: string }
  | { kind: 'not_applied' }
  | { kind: 'checking' }
  | { kind: 'shown_stored' }
  | { kind: 'load_failed' }

export const SAVE_TEXT = {
  saving: 'Saving…',
  saved: 'Saved',
  notApplied: 'Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus.',
  checking: "Couldn't confirm the save. Checking…",
  shownStored: 'Showing what is stored now.',
  loadFailed: "Couldn't load what is stored.",
} as const

const OPEN_LABEL = 'Open Skills & Tools'

/** The key of one setting: "<section>:<name>"; skill trust has no name. */
export function settingKey(section: PermissionSection, name = ''): string {
  return `${section}:${name}`
}

function splitKey(key: string): [PermissionSection, string] {
  const at = key.indexOf(':')
  return [key.slice(0, at) as PermissionSection, key.slice(at + 1)]
}

function toPatch(values: Map<string, SettingValue>): PermissionSettingsPatch {
  const patch: PermissionSettingsPatch = {}
  for (const [key, value] of values) {
    const [section, name] = splitKey(key)
    if (section === 'skill_trust') {
      patch.skill_trust = value as ToolPolicy
      continue
    }
    const map = (patch[section] ??= {}) as Record<string, SettingValue>
    map[name] = value
  }
  return patch
}

function storedValue(stored: PermissionSettings | undefined, key: string): SettingValue | undefined {
  if (!stored) return undefined
  const [section, name] = splitKey(key)
  if (section === 'skill_trust') return stored.skill_trust
  return (stored[section as MapSection] as Record<string, SettingValue>)[name]
}

function withValues(stored: PermissionSettings, values: Map<string, SettingValue>): PermissionSettings {
  const next: PermissionSettings = {
    ...stored,
    policies: { ...stored.policies },
    skill_policies: { ...stored.skill_policies },
    visibility: { ...stored.visibility },
    skill_visibility: { ...stored.skill_visibility },
  }
  for (const [key, value] of values) {
    const [section, name] = splitKey(key)
    if (section === 'skill_trust') next.skill_trust = value as ToolPolicy
    else (next[section as MapSection] as Record<string, SettingValue>)[name] = value
  }
  return next
}

/** "Not saved: <reason>." and the bare reason for the late toast. */
function refusalWords(err: unknown): { rowText: string; reason: string } {
  const raw = isApiError(err) ? serverErrorText(err.body) ?? err.userMessage : String(err)
  const reason = raw.replace(/^Not saved:\s*/i, '').replace(/\.+$/, '')
  return { rowText: `Not saved: ${reason}.`, reason }
}

/** A server answer (any HTTP status) as opposed to no answer at all. */
function isServerAnswer(err: unknown): boolean {
  return isApiError(err) && err.status > 0
}

export class PermissionSaveQueue {
  stored: PermissionSettings | undefined
  loadError: unknown = undefined
  loading = false
  /** Saved batches that changed a skill's permission or level (C-14 Freshness). */
  skillSaves = 0
  private waiting = new Map<string, SettingValue>()
  private sending = new Map<string, SettingValue>()
  private statuses = new Map<string, SaveStatus>()
  private retryBatches = new Map<string, Map<string, SettingValue>>()
  private inFlight = false
  private checking = false
  private timer: ReturnType<typeof setTimeout> | undefined
  private writes = 0
  private pages = 0
  private openPage: (() => void) | undefined
  private listeners = new Set<() => void>()
  private version = 0

  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  getVersion = () => this.version

  private emit() {
    this.version++
    for (const listener of this.listeners) listener()
  }

  /** A page is shown; `open` brings the person back to it from a toast. */
  attach(open: () => void): () => void {
    this.pages++
    this.openPage = open
    return () => {
      this.pages--
    }
  }

  /** The value a row shows: a waiting or sending change, else the stored one. */
  valueOf(section: PermissionSection, name = ''): SettingValue | undefined {
    const key = settingKey(section, name)
    return this.waiting.get(key) ?? this.sending.get(key) ?? storedValue(this.stored, key)
  }

  /** The stored value only, never an unconfirmed change. */
  storedValueOf(section: PermissionSection, name = ''): SettingValue | undefined {
    return storedValue(this.stored, settingKey(section, name))
  }

  statusOf(section: PermissionSection, name = ''): SaveStatus | undefined {
    return this.statuses.get(settingKey(section, name))
  }

  /** True while a change of this key waits or is on its way. */
  isPending(section: PermissionSection, name = ''): boolean {
    const key = settingKey(section, name)
    return this.waiting.has(key) || this.sending.has(key)
  }

  /** Reads what is stored (on every page mount and on a load Retry). */
  async load(): Promise<void> {
    const writesBefore = this.writes
    this.loading = true
    this.emit()
    try {
      const stored = await fetchPermissionSettings()
      // A save answered meanwhile carries newer state than this read.
      if (this.writes === writesBefore) this.stored = stored
      this.loadError = undefined
    } catch (err) {
      this.loadError = err
    } finally {
      this.loading = false
      this.emit()
    }
  }

  /** Queues one or more changes; they are sent together within 1000 ms. */
  change(values: Array<{ section: PermissionSection; name?: string; value: SettingValue }>) {
    for (const { section, name, value } of values) {
      const key = settingKey(section, name)
      this.waiting.set(key, value)
      this.statuses.set(key, { kind: 'saving' })
      this.retryBatches.delete(key)
    }
    this.emit()
    this.schedule()
  }

  /** Retry of a row: re-send its refused or not-applied change, or re-read. */
  retry(section: PermissionSection, name = '') {
    const key = settingKey(section, name)
    const status = this.statuses.get(key)
    if (status?.kind === 'load_failed') {
      void this.recheck([key])
      return
    }
    const batch = this.retryBatches.get(key)
    if (!batch) return
    this.change([...batch].map(([batchKey, value]) => {
      const [batchSection, batchName] = splitKey(batchKey)
      return { section: batchSection, name: batchName, value }
    }))
  }

  private schedule() {
    if (this.timer !== undefined || this.inFlight || this.checking || this.waiting.size === 0) return
    this.timer = setTimeout(() => {
      this.timer = undefined
      void this.send()
    }, 0)
  }

  private async send() {
    if (this.inFlight || this.checking || this.waiting.size === 0) return
    const batch = this.waiting
    this.waiting = new Map()
    this.sending = batch
    this.inFlight = true
    this.emit()
    try {
      const stored = await patchPermissionSettings(toPatch(batch))
      this.writes++
      this.stored = stored
      if ([...batch.keys()].some((key) => key.startsWith('skill_policies:') || key.startsWith('skill_visibility:'))) {
        this.skillSaves++
      }
      this.settle(batch, { kind: 'saved' })
    } catch (err) {
      this.writes++
      if (isApiError(err) && err.status === 503) {
        // Stored, not applied (C-2): the new value is the stored one.
        if (this.stored) this.stored = withValues(this.stored, batch)
        this.settle(batch, { kind: 'not_applied' }, batch)
        this.tellIfAway(`A change on Skills & Tools was saved but is not applied to running agents yet.`)
      } else if (isServerAnswer(err)) {
        const { rowText, reason } = refusalWords(err)
        this.settle(batch, { kind: 'refused', text: rowText }, batch)
        this.tellIfAway(`A change on Skills & Tools was not saved: ${reason}.`)
        await this.check(batch, false)
      } else {
        this.settle(batch, { kind: 'checking' })
        await this.check(batch, true)
      }
    } finally {
      this.inFlight = false
      this.sending = new Map()
      this.emit()
      this.schedule()
    }
  }

  private settle(batch: Map<string, SettingValue>, status: SaveStatus, retryBatch?: Map<string, SettingValue>) {
    for (const key of batch.keys()) {
      // A newer change of the same key keeps its own "Saving…".
      if (this.waiting.has(key)) continue
      this.statuses.set(key, status)
      if (retryBatch) this.retryBatches.set(key, retryBatch)
    }
  }

  /** The fresh GET before the next PATCH (C-16). */
  private async check(batch: Map<string, SettingValue>, unknown: boolean) {
    this.checking = true
    this.sending = new Map()
    this.emit()
    // A failed read is shown (load_failed with Retry) below, never dropped.
    let read = true
    try {
      this.stored = await fetchPermissionSettings()
      this.writes++
    } catch {
      read = false
    } finally {
      this.checking = false
    }
    if (unknown) {
      this.settle(batch, read ? { kind: 'shown_stored' } : { kind: 'load_failed' })
      this.tellIfAway("Couldn't confirm a change on Skills & Tools. Open it to see what is stored.")
    }
  }

  /** Retry after a failed check: read again, never PATCH blindly (D12#9). */
  private async recheck(keys: string[]) {
    const failed = [...this.statuses].filter(([, status]) => status.kind === 'load_failed').map(([key]) => key)
    const affected = new Set([...keys, ...failed])
    for (const key of affected) this.statuses.set(key, { kind: 'checking' })
    this.checking = true
    this.emit()
    try {
      this.stored = await fetchPermissionSettings()
      this.writes++
      for (const key of affected) this.statuses.set(key, { kind: 'shown_stored' })
    } catch {
      for (const key of affected) this.statuses.set(key, { kind: 'load_failed' })
    } finally {
      this.checking = false
      this.emit()
      this.schedule()
    }
  }

  private tellIfAway(message: string) {
    if (this.pages > 0) return
    const open = this.openPage
    useUiStore.getState().addToast({
      message,
      variant: 'warning',
      duration: 0,
      action: { label: OPEN_LABEL, onClick: () => open?.() },
    })
  }
}

const queues = new WeakMap<QueryClient, PermissionSaveQueue>()

/** The app's queue: one per QueryClient, i.e. per app lifetime. */
export function permissionQueueFor(client: QueryClient): PermissionSaveQueue {
  let queue = queues.get(client)
  if (!queue) {
    queue = new PermissionSaveQueue()
    queues.set(client, queue)
  }
  return queue
}

/**
 * The app's permission queue, re-rendering on every change. Pass `open` from
 * a page that shows the settings: it registers the page (late outcomes then
 * stay on the rows instead of a toast) and reads the stored state on mount.
 */
export function usePermissionQueue(open?: () => void): PermissionSaveQueue {
  const client = useQueryClient()
  const queue = permissionQueueFor(client)
  useSyncExternalStore(queue.subscribe, queue.getVersion, queue.getVersion)
  useEffect(() => {
    if (!open) return undefined
    const detach = queue.attach(open)
    void queue.load()
    return detach
    // The page registers once per mount; `open` only names where to go.
  }, [queue])
  return queue
}
