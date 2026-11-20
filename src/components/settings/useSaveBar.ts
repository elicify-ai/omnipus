// useSaveBar — the one save rule of Settings (Settings redesign #1519,
// FR-010 to FR-020, FR-026 to FR-028, FR-033/FR-034).
//
// A section declares its values as SaveBarFields (saved value, label, write
// target, restart and password marks, validation). Edits are drafts in page
// memory only; nothing is stored until Save changes. Save runs, in order:
// validation (focus moves to the first invalid field), the section's pre-save
// confirms, at most one password prompt for the password-marked values, then
// one write per target with only the changed fields. Each write ends in one
// of the four outcomes of saveOutcome.ts; an unknown result runs the
// saved-state check. A value whose outcome is saved (in use or not) stops
// being a draft; a refused or unknown one stays a draft.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { classifyWrite, savedStateCheck, sameValue, type WriteOutcome, type WriteResponse } from './saveOutcome'
import { useSettingsShell } from './SettingsShellContext'

export type SaveBarValue = string | number | boolean | null

export interface SaveBarField { // not-wire-format: screen-side description of one editable setting
  /** Dotted config key (also the key in the write body and in pending-restart). */
  key: string
  /** Visible label (names the setting in prompts, the bar and the restart window). */
  label: string
  /** The saved value as last read. */
  value: SaveBarValue
  kind?: 'select' | 'number' | 'text' | 'switch'
  options?: string[]
  description?: string
  /** Write target when one section writes to several endpoints (FR-015). */
  target?: string
  /** Needs restart badge (FR-037). */
  restart?: boolean
  /** Password badge (FR-025). */
  password?: boolean
  /** Returns the message to show, or null when the value is valid (FR-013). */
  validate?: (value: SaveBarValue) => string | null
  /** Disabled values are neither validated nor sent (FR-040). */
  disabled?: boolean
}

export interface SaveContext { // not-wire-format: per-write context handed to a section's writer
  target: string
  reauthToken?: string
}

export type SaveHandler = (body: Record<string, SaveBarValue>, ctx: SaveContext) => Promise<WriteResponse>

export type FieldMessage = // not-wire-format: screen-only per-field status line
  | { tone: 'error'; text: string }
  | { tone: 'warning'; text: string; keys?: string[] }

export interface UseSaveBarOptions { // not-wire-format: React hook options
  section: string
  fields: SaveBarField[]
  onSave: SaveHandler
  /** Reads the saved (running) value of one key for the saved-state check. */
  readBack?: (key: string) => Promise<unknown>
  /** Pre-save confirms (FR-016). Resolve false to stop the Save. */
  preSave?: (changed: SaveBarField[], drafts: Record<string, SaveBarValue>) => Promise<boolean>
  /** Called after a write ends saved (in use or not) for its keys. */
  onSaved?: (keys: string[]) => void
  onDiscard?: () => void
}

export const SAVED_SIGNAL_MS = 8000

export const NOT_IN_USE_MESSAGE = 'Saved, but not in use yet. Restart the gateway to apply it.'

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

export function fieldDomId(section: string, key: string): string {
  return `settings-${section}-${key}`.replace(/[^a-zA-Z0-9_-]/g, '-')
}

function formatTime(d: Date): string {
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function display(v: unknown): string {
  if (v === null || v === undefined || v === '') return 'empty'
  return typeof v === 'string' ? v : JSON.stringify(v)
}

export function useSaveBar({ section, fields, onSave, readBack, preSave, onSaved, onDiscard }: UseSaveBarOptions) {
  const shell = useSettingsShell()
  const [drafts, setDrafts] = useState<Record<string, SaveBarValue>>({})
  const [knownSaved, setKnownSaved] = useState<Record<string, SaveBarValue>>({})
  const [messages, setMessages] = useState<Record<string, FieldMessage>>({})
  const [failedCount, setFailedCount] = useState(0)
  const [unknownKeys, setUnknownKeys] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [savedAt, setSavedAt] = useState<string | null>(null)
  const beforeRef = useRef<Record<string, SaveBarValue>>({})

  const byKey = useMemo(() => new Map(fields.map((f) => [f.key, f])), [fields])

  const savedValue = useCallback(
    (key: string): SaveBarValue => (key in knownSaved ? knownSaved[key] : (byKey.get(key)?.value ?? null)),
    [knownSaved, byKey],
  )
  const currentValue = useCallback(
    (key: string): SaveBarValue => (key in drafts ? drafts[key] : savedValue(key)),
    [drafts, savedValue],
  )

  const changedKeys = useMemo(
    () => Object.keys(drafts).filter((k) => byKey.has(k) && !byKey.get(k)!.disabled && !sameValue(drafts[k], savedValue(k))),
    [drafts, byKey, savedValue],
  )

  const invalid = useMemo(() => {
    const out: Record<string, string> = {}
    for (const f of fields) {
      if (f.disabled || !f.validate) continue
      const msg = f.validate(currentValue(f.key))
      if (msg) out[f.key] = msg
    }
    return out
  }, [fields, currentValue])
  const invalidKeys = Object.keys(invalid)

  const setDraft = useCallback(
    (key: string, value: SaveBarValue) => {
      setSavedAt(null)
      setDrafts((prev) => {
        const next = { ...prev }
        if (sameValue(value, savedValue(key))) delete next[key]
        else next[key] = value
        return next
      })
      setMessages((prev) => {
        if (!(key in prev) || prev[key].tone === 'warning') return prev
        const next = { ...prev }
        delete next[key]
        return next
      })
    },
    [savedValue],
  )

  const n = changedKeys.length
  const visible = n > 0 || savedAt !== null || unknownKeys.length > 0

  // FR-014 leave guard (in-app half lives in the shell) and the browser's own
  // prompt while drafts exist.
  useEffect(() => {
    shell.setDraftCount(n)
  }, [n, shell])
  useEffect(() => () => shell.setDraftCount(0), [shell])
  useEffect(() => {
    shell.setSaveBarVisible(n > 0)
  }, [n, shell])
  useEffect(() => {
    if (n === 0) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      // Legacy browsers need returnValue set to show the prompt.
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [n])

  // FR-018: the saved signal stays 8 seconds.
  useEffect(() => {
    if (!savedAt) return
    const t = setTimeout(() => setSavedAt(null), SAVED_SIGNAL_MS)
    return () => clearTimeout(t)
  }, [savedAt])

  const focusField = (key: string) => {
    const el = document.querySelector<HTMLElement>(`[data-settings-field="${fieldDomId(section, key)}"]`)
    el?.focus()
  }

  const applyOutcomes = useCallback(
    (outcomes: { keys: string[]; outcome: WriteOutcome }[], sentDrafts: Record<string, SaveBarValue>) => {
      const nextKnown: Record<string, SaveBarValue> = {}
      const nextMessages: Record<string, FieldMessage> = {}
      const resolved: string[] = []
      let failed = 0
      const unknown: string[] = []
      const restartLabels: string[] = []
      for (const { keys, outcome } of outcomes) {
        for (const key of keys) {
          const field = byKey.get(key)
          const label = field?.label ?? key
          switch (outcome.kind) {
            case 'saved':
              nextKnown[key] = sentDrafts[key]
              resolved.push(key)
              if (field?.restart) restartLabels.push(label)
              break
            case 'saved-not-in-use':
              nextKnown[key] = sentDrafts[key]
              resolved.push(key)
              nextMessages[key] = { tone: 'warning', text: NOT_IN_USE_MESSAGE, keys: outcome.keys.length ? outcome.keys : undefined }
              if (field?.restart || outcome.requiresRestart) restartLabels.push(label)
              break
            case 'not-saved':
              failed += 1
              nextMessages[key] = { tone: 'error', text: outcome.message }
              break
            case 'unknown':
              unknown.push(key)
              break
          }
        }
      }
      return { nextKnown, nextMessages, resolved, failed, unknown, restartLabels }
    },
    [byKey],
  )

  const runCheck = useCallback(
    async (keys: string[], sentDrafts: Record<string, SaveBarValue>) => {
      try {
        const states = await savedStateCheck(
          keys.map((key) => ({ key, draft: sentDrafts[key], before: beforeRef.current[key] })),
          readBack,
        )
        const known: Record<string, SaveBarValue> = {}
        const msgs: Record<string, FieldMessage> = {}
        const resolved: string[] = []
        let failed = 0
        for (const key of keys) {
          const st = states[key]
          if (st.kind === 'saved') {
            known[key] = sentDrafts[key]
            resolved.push(key)
            if (!st.inUse) msgs[key] = { tone: 'warning', text: NOT_IN_USE_MESSAGE }
          } else if (st.kind === 'not-saved') {
            failed += 1
            msgs[key] = { tone: 'error', text: 'This change was not saved. Save it again.' }
          } else {
            known[key] = st.savedValue as SaveBarValue
            msgs[key] = {
              tone: 'error',
              text: `This setting was changed somewhere else while you were saving. Saved value: ${display(st.savedValue)}.`,
            }
          }
        }
        return { ok: true as const, known, msgs, resolved, failed }
      } catch {
        return { ok: false as const }
      }
    },
    [readBack],
  )

  const finish = useCallback(
    (
      known: Record<string, SaveBarValue>,
      msgs: Record<string, FieldMessage>,
      resolved: string[],
      failed: number,
      unknown: string[],
      restartLabels: string[],
    ) => {
      setKnownSaved((prev) => ({ ...prev, ...known }))
      setMessages((prev) => {
        const next = { ...prev }
        for (const k of resolved) delete next[k]
        return { ...next, ...msgs }
      })
      setDrafts((prev) => {
        const next = { ...prev }
        for (const k of resolved) delete next[k]
        return next
      })
      setFailedCount(failed)
      setUnknownKeys(unknown)
      if (resolved.length) onSaved?.(resolved)
      shell.refreshPendingRestart()
      const anyError = Object.values(msgs).some((m) => m.tone === 'error')
      if (failed === 0 && unknown.length === 0 && !anyError) {
        setSavedAt(formatTime(new Date()))
        shell.announce('Changes saved.' + (restartLabels.length ? ' Some need a gateway restart.' : ''))
      }
      if (restartLabels.length) shell.offerRestart(restartLabels)
    },
    [onSaved, shell],
  )

  const save = useCallback(async () => {
    if (saving) return
    if (invalidKeys.length) {
      focusField(invalidKeys[0])
      shell.announce(`Fix ${plural(invalidKeys.length, 'value', 'values')} before saving.`)
      return
    }
    const changed = changedKeys.map((k) => byKey.get(k)!)
    if (!changed.length) return
    const sentDrafts: Record<string, SaveBarValue> = {}
    for (const f of changed) {
      sentDrafts[f.key] = drafts[f.key]
      beforeRef.current[f.key] = savedValue(f.key)
    }
    if (preSave && !(await preSave(changed, sentDrafts))) return
    let token: string | undefined
    const protectedFields = changed.filter((f) => f.password)
    if (protectedFields.length && shell.requestPassword) {
      const got = await shell.requestPassword(protectedFields.map((f) => f.label))
      if (got === null) return // Cancel stores nothing and keeps every draft (FR-029).
      token = got
    }
    setSaving(true)
    setUnknownKeys([])
    try {
      const targets = new Map<string, SaveBarField[]>()
      for (const f of changed) {
        const t = f.target ?? 'default'
        targets.set(t, [...(targets.get(t) ?? []), f])
      }
      const outcomes: { keys: string[]; outcome: WriteOutcome }[] = []
      for (const [target, group] of targets) {
        const body: Record<string, SaveBarValue> = {}
        for (const f of group) body[f.key] = sentDrafts[f.key]
        const usesToken = group.some((f) => f.password)
        const outcome = await classifyWrite(() => onSave(body, { target, reauthToken: usesToken ? token : undefined }))
        outcomes.push({ keys: group.map((f) => f.key), outcome })
      }
      const a = applyOutcomes(outcomes, sentDrafts)
      if (a.unknown.length) {
        const c = await runCheck(a.unknown, sentDrafts)
        if (c.ok) {
          finish({ ...a.nextKnown, ...c.known }, { ...a.nextMessages, ...c.msgs }, [...a.resolved, ...c.resolved], a.failed + c.failed, [], a.restartLabels)
        } else {
          finish(a.nextKnown, a.nextMessages, a.resolved, a.failed, a.unknown, a.restartLabels)
        }
      } else {
        finish(a.nextKnown, a.nextMessages, a.resolved, a.failed, [], a.restartLabels)
      }
    } finally {
      setSaving(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saving, invalidKeys, changedKeys, byKey, drafts, savedValue, preSave, shell, onSave, applyOutcomes, runCheck, finish])

  const retry = useCallback(async () => {
    const keys = unknownKeys
    if (!keys.length) return
    const sentDrafts: Record<string, SaveBarValue> = {}
    for (const k of keys) sentDrafts[k] = drafts[k]
    const c = await runCheck(keys, sentDrafts)
    if (c.ok) finish(c.known, c.msgs, c.resolved, failedCount + c.failed, [], [])
  }, [unknownKeys, drafts, runCheck, finish, failedCount])

  const discard = useCallback(() => {
    setDrafts({})
    setMessages((prev) => {
      const next: Record<string, FieldMessage> = {}
      for (const [k, m] of Object.entries(prev)) if (m.tone === 'warning') next[k] = m
      return next
    })
    setFailedCount(0)
    setUnknownKeys([])
    onDiscard?.()
  }, [onDiscard])

  const restartCount = changedKeys.filter((k) => byKey.get(k)?.restart).length
  const passwordCount = changedKeys.filter((k) => byKey.get(k)?.password).length

  return {
    section,
    drafts,
    value: currentValue,
    savedValue,
    setDraft,
    message: (key: string): FieldMessage | undefined =>
      invalid[key] && byKey.has(key) ? { tone: 'error', text: invalid[key] } : messages[key],
    isDraft: (key: string) => changedKeys.includes(key),
    changedKeys,
    invalidKeys,
    save,
    discard,
    retry,
    bar: {
      visible,
      count: n,
      restartCount,
      passwordCount,
      invalidCount: invalidKeys.length,
      failedCount,
      unknownCount: unknownKeys.length,
      saving,
      savedAt,
      onSave: save,
      onDiscard: discard,
      onRetry: retry,
    },
  }
}

export type SaveBarState = ReturnType<typeof useSaveBar>
export type SaveBarPanelProps = SaveBarState['bar']
