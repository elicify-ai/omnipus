// useEffortSwitch.ts — the one model-switch transaction every shared-picker
// call site uses (effort-levels-spec-r10.md, "Mapping table" → Picker, FR-047,
// FR-053, FR-054, S-09 to S-11, S-14, S-73, S-74, S-86).
//
// When the person picks a new model for a slot:
//   - the slot's EFFECTIVE value (its own, or what a Not set slot inherits) is
//     mapped against the target's served capabilities (mapEffort);
//   - a blocked result keeps the previous model and opens the pick-a-level (or
//     "Let the provider decide" / Cancel) step — nothing is committed;
//   - otherwise the model and the mapped value are committed together in one
//     change (the caller saves them in one write), with the S-09 note when the
//     value changed. A Not set slot is never rewritten silently: it stays Not
//     set and the chip shows what it is sent as.

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { effortSwitchNote, mapEffort } from './mapEffort'
import { effortTargetOf, type ServedRow } from './servedReasoning'

export interface EffortPair { // not-wire-format: in-memory slot key passed between the picker hook and call sites; callers copy its fields into their own request bodies
  provider: string
  model: string
}

export interface EffortSlotValue extends EffortPair { // not-wire-format: in-memory slot value held by the switch hook; callers copy its fields into their own request bodies
  /** Stored effort: '' (Not set), 'provider_default', a level, 'on', 'off'. */
  effort: string
}

/** A switch waiting on the blocked step. */
export interface EffortPendingSwitch extends EffortPair { // not-wire-format: React state for the blocked switch step of the picker; never leaves the browser
  /** 'levels': pick a level (nearest marked, not chosen); 'nothing_sendable': "Let the provider decide" or Cancel. */
  kind: 'levels' | 'nothing_sendable'
  nearest: string
}

export interface EffortSwitchOptions { // not-wire-format: hook options (callbacks and lookup function) for useEffortSwitch; not serialisable at all
  value: EffortSlotValue
  /** The value a Not set slot resolves to (chat: the agent's setting; a riding primary: the default model's). */
  inherit?: string
  /** Recap slots: absent means Off (FD-19). */
  defaultEffort?: 'off'
  /** Served catalog row for a pair. */
  lookup: (pair: EffortPair) => ServedRow
  /**
   * The served catalog has loaded. Until it has, nothing can be mapped, so a
   * switch keeps the stored value as it is instead of treating the target
   * as a model that does not reason. Defaults to true.
   */
  ready?: boolean
  /** Commits the model and its effort together (one write). `note` is the S-09 note, or null. */
  onCommit: (pair: EffortPair, effort: string, note: string | null) => void
}

export interface EffortSwitch { // not-wire-format: hook return value (state plus handlers) for useEffortSwitch; not serialisable at all
  pending: EffortPendingSwitch | null
  /** One-shot polite announcement for the picker's live region. */
  announcement: string
  /** The model was picked: map, commit or open the blocked step. */
  requestSwitch: (pair: EffortPair) => void
  /** The blocked step was answered with a level or Provider default. */
  completePending: (effort: string) => void
  /** The blocked step was cancelled (Escape or Cancel): nothing changes. */
  cancelPending: () => void
  /** Wire to ModelSelector's onChange / onPairChange: one switch per pick. */
  selectorHandlers: {
    onChange: (model: string) => void
    onPairChange: (pair: EffortPair) => void
  }
}

export function useEffortSwitch({ value, inherit, defaultEffort, lookup, onCommit, ready = true }: EffortSwitchOptions): EffortSwitch {
  const [pending, setPending] = useState<EffortPendingSwitch | null>(null)
  const [announcement, setAnnouncement] = useState('')

  const requestSwitch = useCallback(
    (pair: EffortPair) => {
      if (!ready) {
        setPending(null)
        setAnnouncement('')
        onCommit(pair, value.effort, null)
        return
      }
      const own = value.effort !== '' ? value.effort : (defaultEffort ?? '')
      const effective = own !== '' ? own : (inherit ?? '')
      const row = lookup(pair)
      const mapping = mapEffort(effective, effortTargetOf(row))
      if (mapping.blocked) {
        setPending({
          provider: pair.provider,
          model: pair.model,
          kind: mapping.providerDefaultEscape ? 'nothing_sendable' : 'levels',
          nearest: mapping.nearest,
        })
        // The step's own text is read as focus moves into it; the live
        // region stays quiet so the sentence exists once.
        setAnnouncement('')
        return
      }
      setPending(null)
      if (own === '') {
        // Not set stays Not set; the chip reports what it is sent as.
        onCommit(pair, value.effort, null)
        setAnnouncement(effortTargetOf(row).reasons ? '' : `${pair.model} has no effort setting.`)
        return
      }
      const note = mapping.noteKind ? effortSwitchNote(mapping.noteKind, pair.model, effective, mapping.value) : null
      // A recap slot riding its Off default keeps riding it when the target can keep Off.
      const ridesDefaultOff = value.effort === '' && (mapping.value === 'off' || mapping.value === 'none')
      onCommit(pair, ridesDefaultOff ? '' : mapping.value, note)
      // A remapping note is shown (and announced) by the control's live region.
      setAnnouncement(effortTargetOf(row).reasons ? '' : `${pair.model} has no effort setting.`)
    },
    [value.effort, defaultEffort, inherit, lookup, onCommit, ready],
  )

  const completePending = useCallback(
    (effort: string) => {
      if (!pending) return
      setPending(null)
      setAnnouncement('')
      onCommit({ provider: pending.provider, model: pending.model }, effort, null)
    },
    [pending, onCommit],
  )

  const cancelPending = useCallback(() => {
    if (!pending) return
    setPending(null)
    setAnnouncement('Switch cancelled')
  }, [pending])

  // ModelSelector fires onChange(model) and then, for every listed row,
  // onPairChange({model, provider}) in the same tick; a free-typed slug fires
  // onChange alone. Switch once per pick: on the pair when it arrives, else on
  // the bare slug (provider unknown).
  const requestRef = useRef(requestSwitch)
  useLayoutEffect(() => {
    requestRef.current = requestSwitch
  }, [requestSwitch])
  const pairSeenRef = useRef(false)
  const selectorHandlers = useMemo(
    () => ({
      onChange: (model: string) => {
        pairSeenRef.current = false
        queueMicrotask(() => {
          if (!pairSeenRef.current) requestRef.current({ provider: '', model })
        })
      },
      onPairChange: (pair: EffortPair) => {
        pairSeenRef.current = true
        requestRef.current({ provider: pair.provider, model: pair.model })
      },
    }),
    [],
  )

  return { pending, announcement, requestSwitch, completePending, cancelPending, selectorHandlers }
}
