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
import { classifyWrite, type PartCheck, type WriteOutcome, type WriteResponse } from './saveOutcome'
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
import { assertOneProtectedEndpointPerSection } from './savePlanner'

export interface FormSavePart { // not-wire-format: one endpoint's share of a section Save
  /** Stable name of the part (its endpoint). */
  id: string
  /** Number of changed values in the part. */
  count: number
  /**
   * True while the part's read failed (FR-066). Its drafts stay (they still
   * count for the leave guard), but Save leaves the part out. Asked again
   * right before the part's write, so a read that fails during the password
   * prompt still blocks it.
   */
  blocked?: () => boolean
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
  /**
   * Saved-state check after an unknown result (C-6, FR-020 outcome 4): the
   * part classifies its sent values with GET /config/pending-restart plus its
   * section read (`checkFormPart`). On "changed somewhere else" the part
   * itself takes the read values as its saved values and keeps its drafts.
   * A failed read throws (the result stays unknown).
   */
  check?: () => Promise<PartCheck>
  /**
   * Runs after this part's write reached an outcome. On a refusal it may
   * report how many of the part's changes it took back out of the drafts
   * (FR-015a: a refused new sandbox path returns to its add input); those
   * are no longer unsaved changes and are not counted as failed.
   */
  onOutcome?: (outcome: WriteOutcome) => { recovered: number } | void
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

/** A part's outcome after a Save: a write outcome, or a value changed elsewhere. */
type PartOutcome = WriteOutcome | { kind: 'changed-elsewhere'; text: string }

/** A part with drafts whose read has not failed: Save writes it. */
function writable(p: FormSavePart): boolean {
  return p.count > 0 && !p.blocked?.()
}

export function useFormSaveBar(parts: FormSavePart[]) {
  const shell = useSettingsShell()
  const [saving, setSaving] = useState(false)
  const [savedAt, setSavedAt] = useState<string | null>(null)
  const [results, setResults] = useState<Record<string, PartResult>>({})

  const count = parts.reduce((n, p) => n + p.count, 0)
  const invalidCount = parts.reduce((n, p) => n + p.invalidCount, 0)
  const restartLabels = useMemo(() => parts.flatMap((p) => (writable(p) ? p.restartLabels ?? [] : [])), [parts])
  const passwordLabels = useMemo(() => parts.flatMap((p) => (writable(p) ? p.passwordLabels ?? [] : [])), [parts])
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

  const resultOf = (outcome: PartOutcome, n: number): PartResult => {
    switch (outcome.kind) {
      case 'changed-elsewhere':
        // The draft stays an unsaved change; the field names the saved value.
        return { failed: 0, unknown: 0, message: { tone: 'error', text: outcome.text } }
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

  const checkUnknown = useCallback(async (part: FormSavePart): Promise<PartOutcome | null> => {
    if (!part.check) return null
    try {
      const state = await part.check()
      switch (state.kind) {
        case 'saved':
          return state.inUse
            ? { kind: 'saved', body: undefined }
            : { kind: 'saved-not-in-use', keys: [], body: undefined, requiresRestart: true }
        case 'not-saved':
          return { kind: 'not-saved', message: 'These changes were not saved. Save them again.', status: 0, body: undefined }
        case 'changed-elsewhere':
          return { kind: 'changed-elsewhere', text: state.text }
        case 'unknown':
          return null
      }
    } catch {
      return null
    }
  }, [])

  const finish = useCallback(
    (outcomes: { part: FormSavePart; outcome: PartOutcome; n: number }[]) => {
      const next: Record<string, PartResult> = {}
      let restart = false
      let notInUse = false
      for (const { part, outcome, n } of outcomes) {
        const effect = outcome.kind !== 'changed-elsewhere' ? part.onOutcome?.(outcome) : undefined
        next[part.id] = resultOf(outcome, Math.max(0, n - (effect?.recovered ?? 0)))
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
    // FR-066: a part whose read failed is left out; its drafts stay.
    const changed = parts.filter(writable)
    if (!changed.length) {
      if (parts.some((p) => p.count > 0)) shell.announce('Nothing was saved. The changed settings could not be loaded; load them again first.')
      return
    }
    // FR-016: every pre-save confirm runs before the password prompt.
    for (const p of changed) if (p.preSave && !(await p.preSave())) return
    // FR-030 / C-4: one Save has one consent token, so the password-marked
    // values may come from one part (one protected endpoint) only.
    try {
      assertOneProtectedEndpointPerSection({ form: changed.filter((p) => p.passwordLabels?.length).map((p) => p.id) })
    } catch (err) {
      shell.announce(`Nothing was saved. ${err instanceof Error ? err.message : String(err)}`)
      return
    }
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
      const outcomes: { part: FormSavePart; outcome: PartOutcome; n: number }[] = []
      for (const part of ordered) {
        if (part.blocked?.()) continue
        const usesToken = !!part.passwordLabels?.length
        const written = await classifyWrite(() => part.write({ reauthToken: usesToken ? reauthToken : undefined }))
        const outcome: PartOutcome = written.kind === 'unknown' ? ((await checkUnknown(part)) ?? written) : written
        outcomes.push({ part, outcome, n: part.count })
        if (outcome.kind !== 'changed-elsewhere' && part.stopsSave?.(outcome)) break
      }
      finish(outcomes)
    } finally {
      setSaving(false)
    }
  }, [saving, invalidCount, parts, passwordLabels, shell, checkUnknown, finish])

  const retry = useCallback(async () => {
    const pending = parts.filter((p) => (results[p.id]?.unknown ?? 0) > 0)
    if (!pending.length) return
    const outcomes: { part: FormSavePart; outcome: PartOutcome; n: number }[] = []
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
