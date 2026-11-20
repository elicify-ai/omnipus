// useFormSaveBar — the Save bar for a section whose drafts live in whole
// forms rather than flat key/value rows (Settings redesign #1519, FR-010 to
// FR-020): Limits (Context and tool results with its Model overrides list,
// plus Agents and goals) and Third-party agents.
//
// A section passes one "part" per endpoint it writes. Each part keeps its own
// form state and says how many values changed, how many are invalid, and how
// to write and discard them. This hook supplies the shared behaviour:
//   - one Save bar for the whole section (FR-019) with the page's leave
//     prompt and gold-button rule (FR-014, FR-017);
//   - pre-save confirms before the password prompt (FR-016);
//   - one password prompt per Save (FR-026), the token handed only to the
//     part that holds the password-marked values (FR-027);
//   - the protected part writes first; a refusal that asks for a new confirm
//     (the lowering 409) stops every other write of that Save (FR-031);
//   - the four outcomes of FR-020 per part, with the saved-state check after
//     an unknown result;
//   - "Saved at HH:MM" for 8 seconds and the restart offer (FR-018, FR-033).

import { useCallback, useEffect, useMemo, useState } from 'react'
import { classifyWrite, type WriteOutcome, type WriteResponse } from './saveOutcome'
import { useSettingsShell } from './SettingsShellContext'
import { useShellDrafts } from './useShellDrafts'
import {
  formatTime,
  NOT_IN_USE_MESSAGE,
  plural,
  SAVED_SIGNAL_MS,
  type FieldMessage,
  type SaveBarPanelProps,
} from './useSaveBar'

export interface FormSavePart { // not-wire-format: one endpoint's share of a section Save
  /** Stable name of the part (its endpoint). */
  id: string
  /** Number of changed values in the part. */
  count: number
  /** Number of changed or shown values that fail the screen's own checks. */
  invalidCount: number
  /** Visible labels of the changed values that need a gateway restart. */
  restartLabels?: string[]
  /** Visible labels of the changed values whose write needs the password (FR-026). */
  passwordLabels?: string[]
  /** Pre-save confirm (FR-016). Resolve false to stop the whole Save. */
  preSave?: () => Promise<boolean>
  /** Sends this part's one write. */
  write: (ctx: { reauthToken?: string }) => Promise<WriteResponse>
  /**
   * True when a refused write needs a new confirm (FR-031): no other write
   * of this Save is sent.
   */
  stopsSave?: (outcome: WriteOutcome) => boolean
  /** Saved-state check after an unknown result (C-6). */
  check?: () => Promise<'saved' | 'not-saved'>
  /** Runs after this part's write reached an outcome. */
  onOutcome?: (outcome: WriteOutcome) => void
  /** Moves focus to the part's first invalid value. */
  focusInvalid?: () => void
  /** Puts every value of the part back to its saved value. */
  discard: () => void
}

interface PartResult { // not-wire-format: screen state of one part after a Save
  failed: number
  unknown: number
  message: FieldMessage | null
}

const EMPTY: PartResult = { failed: 0, unknown: 0, message: null }

export function useFormSaveBar(parts: FormSavePart[]) {
  const shell = useSettingsShell()
  const [saving, setSaving] = useState(false)
  const [savedAt, setSavedAt] = useState<string | null>(null)
  const [results, setResults] = useState<Record<string, PartResult>>({})

  const count = parts.reduce((n, p) => n + p.count, 0)
  const invalidCount = parts.reduce((n, p) => n + p.invalidCount, 0)
  const restartLabels = useMemo(() => parts.flatMap((p) => (p.count ? p.restartLabels ?? [] : [])), [parts])
  const passwordLabels = useMemo(() => parts.flatMap((p) => (p.count ? p.passwordLabels ?? [] : [])), [parts])
  const failedCount = Object.values(results).reduce((n, r) => n + r.failed, 0)
  const unknownCount = Object.values(results).reduce((n, r) => n + r.unknown, 0)

  useShellDrafts(count)

  // A new edit ends the saved signal.
  useEffect(() => {
    if (count > 0) setSavedAt(null)
  }, [count])
  useEffect(() => {
    if (!savedAt) return
    const t = setTimeout(() => setSavedAt(null), SAVED_SIGNAL_MS)
    return () => clearTimeout(t)
  }, [savedAt])

  const resultOf = (outcome: WriteOutcome, n: number): PartResult => {
    switch (outcome.kind) {
      case 'saved':
        return EMPTY
      case 'saved-not-in-use':
        return { failed: 0, unknown: 0, message: { tone: 'warning', text: NOT_IN_USE_MESSAGE, keys: outcome.keys.length ? outcome.keys : undefined } }
      case 'not-saved':
        return { failed: n, unknown: 0, message: { tone: 'error', text: outcome.message } }
      case 'unknown':
        return { failed: 0, unknown: n, message: null }
    }
  }

  const checkUnknown = useCallback(async (part: FormSavePart): Promise<WriteOutcome | null> => {
    if (!part.check) return null
    try {
      const state = await part.check()
      return state === 'saved'
        ? { kind: 'saved', body: undefined }
        : { kind: 'not-saved', message: 'These changes were not saved. Save them again.', status: 0, body: undefined }
    } catch {
      return null
    }
  }, [])

  const finish = useCallback(
    (outcomes: { part: FormSavePart; outcome: WriteOutcome; n: number }[]) => {
      const next: Record<string, PartResult> = {}
      let restart = false
      let notInUse = false
      for (const { part, outcome, n } of outcomes) {
        part.onOutcome?.(outcome)
        next[part.id] = resultOf(outcome, n)
        if (outcome.kind === 'saved' || outcome.kind === 'saved-not-in-use') {
          if (part.restartLabels?.length) restart = true
          if (outcome.kind === 'saved-not-in-use') {
            notInUse = true
            if (outcome.requiresRestart) restart = true
          }
        }
      }
      setResults((prev) => ({ ...prev, ...next }))
      shell.refreshPendingRestart()
      const allSaved = outcomes.every(({ outcome }) => outcome.kind === 'saved' || outcome.kind === 'saved-not-in-use')
      if (allSaved && outcomes.length) {
        setSavedAt(formatTime(new Date()))
        shell.announce('Changes saved.' + (restart ? ' Some need a gateway restart.' : notInUse ? ` ${NOT_IN_USE_MESSAGE}` : ''))
      }
      if (restart) shell.offerRestart(restartLabels)
    },
    [shell, restartLabels],
  )

  const save = useCallback(async () => {
    if (saving) return
    if (invalidCount > 0) {
      parts.find((p) => p.invalidCount > 0)?.focusInvalid?.()
      shell.announce(`Fix ${plural(invalidCount, 'value', 'values')} before saving.`)
      return
    }
    const changed = parts.filter((p) => p.count > 0)
    if (!changed.length) return
    // FR-016: every pre-save confirm runs before the password prompt.
    for (const p of changed) if (p.preSave && !(await p.preSave())) return
    let reauthToken: string | undefined
    if (passwordLabels.length && shell.requestPassword) {
      const got = await shell.requestPassword(passwordLabels)
      if (got === null) return // Cancel stores nothing and keeps every draft (FR-029).
      reauthToken = got
    }
    // The protected part writes first so a refusal that needs a new confirm
    // stops the rest (FR-031).
    const ordered = [...changed].sort((a, b) => Number(!!b.passwordLabels?.length) - Number(!!a.passwordLabels?.length))
    setSaving(true)
    try {
      const outcomes: { part: FormSavePart; outcome: WriteOutcome; n: number }[] = []
      for (const part of ordered) {
        const usesToken = !!part.passwordLabels?.length
        let outcome = await classifyWrite(() => part.write({ reauthToken: usesToken ? reauthToken : undefined }))
        if (outcome.kind === 'unknown') outcome = (await checkUnknown(part)) ?? outcome
        outcomes.push({ part, outcome, n: part.count })
        if (part.stopsSave?.(outcome)) break
      }
      finish(outcomes)
    } finally {
      setSaving(false)
    }
  }, [saving, invalidCount, parts, passwordLabels, shell, checkUnknown, finish])

  const retry = useCallback(async () => {
    const pending = parts.filter((p) => (results[p.id]?.unknown ?? 0) > 0)
    if (!pending.length) return
    const outcomes: { part: FormSavePart; outcome: WriteOutcome; n: number }[] = []
    for (const part of pending) {
      const outcome = await checkUnknown(part)
      if (outcome) outcomes.push({ part, outcome, n: results[part.id].unknown })
    }
    if (outcomes.length) finish(outcomes)
    else shell.announce('The gateway still did not answer. Check that it is running, then retry.')
  }, [parts, results, checkUnknown, finish, shell])

  const discard = useCallback(() => {
    for (const p of parts) p.discard()
    setResults((prev) => {
      const next: Record<string, PartResult> = {}
      for (const [id, r] of Object.entries(prev)) if (r.message?.tone === 'warning') next[id] = { ...EMPTY, message: r.message }
      return next
    })
  }, [parts])

  const bar: SaveBarPanelProps = {
    visible: count > 0 || savedAt !== null || unknownCount > 0,
    count,
    restartCount: restartLabels.length,
    passwordCount: passwordLabels.length,
    invalidCount,
    failedCount,
    unknownCount,
    saving,
    savedAt,
    onSave: save,
    onDiscard: discard,
    onRetry: retry,
  }

  return {
    bar,
    /** The latest outcome line of one part (an error or "Saved, but not in use yet"). */
    message: (id: string): FieldMessage | null => results[id]?.message ?? null,
    save,
  }
}
