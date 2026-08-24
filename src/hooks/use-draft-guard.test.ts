// TDD row 9 — TestDraftGuard (ADR-068 FR-033).
//
// Oracle: the "Close behaviour by draft state" scenario outline in
// docs/internal/specs/adr-068-providers-ux-spec.md, all five rows, plus the
// FR-033 sentence "whitespace = empty; saved = clean".

import { describe, expect, it } from 'vitest'
import {
  DRAFT_DISCARD_PROMPT,
  type DraftCloseAction,
  draftCloseDecision,
  isDraftDirty,
  resolveDiscardPrompt,
  shouldGuardClose,
} from './use-draft-guard'

describe('TestDraftGuard', () => {
  // value | saved | action | result — the spec's Examples table, unchanged.
  const outline: Array<[string, boolean, DraftCloseAction, 'close' | 'prompt']> = [
    ['', false, 'esc', 'close'],
    ['   ', false, 'esc', 'close'],
    ['sk-x', true, 'esc', 'close'],
    ['sk-x', false, 'overlay', 'prompt'],
    ['sk-x', false, 'cancel', 'close'],
  ]

  it.each(outline)('value %j saved=%s via %s -> %s', (value, saved, action, outcome) => {
    const decision = draftCloseDecision({ value, saved, action })
    expect(decision.outcome).toBe(outcome)
    expect(decision.prompt).toBe(outcome === 'prompt')
    // The draft survives exactly while the prompt is up, and only then.
    expect(decision.clearDraft).toBe(outcome === 'close')
  })

  it('prompts on Esc as well as on an overlay click — both are accidental closes', () => {
    for (const action of ['esc', 'overlay'] as DraftCloseAction[]) {
      expect(shouldGuardClose({ value: 'sk-live-1', saved: false, action })).toBe(true)
    }
  })

  it('never prompts on an explicit Cancel, however dirty the draft', () => {
    const decision = draftCloseDecision({ value: 'sk-live-1', saved: false, action: 'cancel' })
    expect(decision).toEqual({ outcome: 'close', prompt: false, clearDraft: true })
  })

  it('treats whitespace as empty', () => {
    for (const value of ['', ' ', '   ', '\t', '\n ']) {
      expect(isDraftDirty(value, false)).toBe(false)
      expect(shouldGuardClose({ value, saved: false, action: 'esc' })).toBe(false)
    }
    expect(isDraftDirty(' sk-x ', false)).toBe(true)
  })

  it('treats a saved value as clean', () => {
    expect(isDraftDirty('sk-x', true)).toBe(false)
    expect(shouldGuardClose({ value: 'sk-x', saved: true, action: 'overlay' })).toBe(false)
  })

  it('treats a null or undefined field as empty', () => {
    expect(isDraftDirty(null, false)).toBe(false)
    expect(isDraftDirty(undefined, false)).toBe(false)
    expect(draftCloseDecision({ value: null, saved: false, action: 'esc' }).outcome).toBe('close')
  })

  it('closes and clears on Discard, and stays open on Keep editing', () => {
    expect(resolveDiscardPrompt(true)).toEqual({ outcome: 'close', prompt: false, clearDraft: true })
    expect(resolveDiscardPrompt(false)).toEqual({ outcome: 'prompt', prompt: true, clearDraft: false })
  })

  it('carries the prompt copy the spec names', () => {
    expect(DRAFT_DISCARD_PROMPT.title).toBe('Discard key?')
    expect(DRAFT_DISCARD_PROMPT.confirm).toBe('Discard')
    expect(DRAFT_DISCARD_PROMPT.cancel).toBe('Keep editing')
  })
})
