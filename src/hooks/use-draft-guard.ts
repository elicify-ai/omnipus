// use-draft-guard.ts — ADR-068 FR-033 / §5 item 7: what happens when the
// provider config sheet is closed while an API key has been typed but not
// saved.
//
// Today `handleClose` clears the draft on Esc and on an overlay click, so a
// mis-aimed click silently destroys a pasted key. The rule this module encodes:
// an accidental close (Esc, overlay) with a dirty draft does not close at all —
// it asks. An explicit Cancel closes and clears, because the operator said so.
// A clean draft closes silently either way, and whitespace counts as clean:
// nobody means to keep "   ".
//
// Pure functions only (T068-19 DoD) — no React, no state. The sheet component
// (T068-24) calls `draftCloseDecision` from its own close handler; keeping the
// rule out of the component is what lets the five spec rows be tested directly.

/** How the operator tried to close the sheet. */
export type DraftCloseAction = 'esc' | 'overlay' | 'cancel'

/** What the sheet must do. */
export type DraftCloseOutcome = 'close' | 'prompt'

/** FR-033 copy for the inline discard prompt. */
export const DRAFT_DISCARD_PROMPT = Object.freeze({
  title: 'Discard key?',
  confirm: 'Discard',
  cancel: 'Keep editing',
})

export interface DraftCloseInput {
  /** The current contents of the key field. */
  value: string | null | undefined
  /** True when what is in the field has already been saved. */
  saved: boolean
  action: DraftCloseAction
}

export interface DraftCloseDecision {
  outcome: DraftCloseOutcome
  /** True when the sheet stays mounted and shows DRAFT_DISCARD_PROMPT. */
  prompt: boolean
  /** True when the draft may be dropped — never true while prompting. */
  clearDraft: boolean
}

/**
 * A draft is dirty when it holds a key the operator would lose. Whitespace is
 * not a key (FR-033, "whitespace = empty"), and an already-saved value is not a
 * loss (FR-033, "saved = clean").
 */
export function isDraftDirty(value: string | null | undefined, saved: boolean): boolean {
  if (saved) return false
  return (value ?? '').trim().length > 0
}

/**
 * The FR-033 close matrix:
 *
 *   ""     / any    / Esc or overlay -> close, no prompt
 *   "   "  / any    / Esc or overlay -> close, no prompt (whitespace = empty)
 *   "sk-x" / saved  / Esc or overlay -> close, no prompt (saved = clean)
 *   "sk-x" / unsaved/ Esc or overlay -> STAY OPEN, prompt
 *   any    / any    / Cancel         -> close, no prompt, draft cleared
 */
export function draftCloseDecision(input: DraftCloseInput): DraftCloseDecision {
  const dirty = isDraftDirty(input.value, input.saved)
  if (input.action !== 'cancel' && dirty) {
    return { outcome: 'prompt', prompt: true, clearDraft: false }
  }
  return { outcome: 'close', prompt: false, clearDraft: true }
}

/** True when closing needs to ask first — the condition a sheet guards on. */
export function shouldGuardClose(input: DraftCloseInput): boolean {
  return draftCloseDecision(input).outcome === 'prompt'
}

/** What *Discard* does once the prompt is up: close and drop the draft. */
export function resolveDiscardPrompt(confirmed: boolean): DraftCloseDecision {
  return confirmed
    ? { outcome: 'close', prompt: false, clearDraft: true }
    : { outcome: 'prompt', prompt: true, clearDraft: false }
}
