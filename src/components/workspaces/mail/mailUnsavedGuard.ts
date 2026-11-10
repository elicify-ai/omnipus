// mailUnsavedGuard.ts — cross-component "don't silently discard an unsaved
// Mail edit" guard (CRIT-001, wired into the shared shell's leave gate via
// mailPanelDefinition's beforeLeave/beforeLeaveRequired — same singleton
// pattern as Library's `preview/unsavedGuard.ts`, adapted for Mail's two
// independent unsaved-edit surfaces).
//
// Mail can carry unsaved text in two places at once — the compose dialog
// (MailComposeDialog) and the draft editor inside MailPreviewPane — and
// unlike Library's single editor, opening one does not close the other (the
// Compose button in MailPanel's header stays reachable while a draft is
// being edited in the preview pane). A single shared boolean would let one
// surface's "I'm clean now" wipe out the other's still-unsaved text, so
// dirtiness is tracked per named source and OR'd together.
export type MailUnsavedSource = 'compose' | 'draft'

const dirtySources = new Set<MailUnsavedSource>()

/** Called by the compose dialog and the draft editor whenever their own
 * unsaved-text state changes, and by their close/unmount cleanup to clear
 * their own source. */
export function setMailEditorDirty(source: MailUnsavedSource, isDirty: boolean): void {
  if (isDirty) dirtySources.add(source)
  else dirtySources.delete(source)
}

export function isMailEditorDirty(): boolean {
  return dirtySources.size > 0
}

// ── In-app discard-confirmation dialog ─────────────────────────────────────
// Same external-store shape as Library's unsavedGuard.ts: confirmDiscardMailEdits
// is called from the shell's leave gate (a plain function, not a component),
// so it flips a module-level store and MailPanel — always mounted while Mail
// is open, in both docked and full-screen presentation — renders the ONE
// dialog that answers every pending call.
let open = false
const listeners = new Set<() => void>()
// FIFO of every caller currently waiting on the ONE dialog that is open —
// same rationale as Library: more than one leave attempt can arrive before
// the user answers.
let pendingResolvers: Array<(result: boolean) => void> = []

function emit(): void {
  for (const listener of listeners) listener()
}

export function subscribeMailDiscardConfirmDialog(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getMailDiscardConfirmDialogOpen(): boolean {
  return open
}

/**
 * Returns a Promise that resolves true if it's safe to proceed (nothing
 * unsaved, or the user confirmed discarding it) — clearing every dirty
 * source in that case — and false if the user chose to stay. Every caller
 * MUST await this: an un-awaited call used in `!expr` is always truthy.
 */
export function confirmDiscardMailEdits(): Promise<boolean> {
  if (!isMailEditorDirty()) return Promise.resolve(true)
  const promise = new Promise<boolean>((resolve) => {
    pendingResolvers.push(resolve)
  })
  if (!open) {
    open = true
    emit()
  }
  return promise
}

/** Called by the dialog host (MailPanel) once the user answers — Discard
 * (`true`), or Cancel/Escape/outside-click (`false`). Answers every pending
 * confirmDiscardMailEdits() call with the same result. */
export function resolveMailDiscardConfirmDialog(result: boolean): void {
  if (result) dirtySources.clear()
  open = false
  const resolvers = pendingResolvers
  pendingResolvers = []
  emit()
  for (const resolve of resolvers) resolve(result)
}

/** MailPanel's unmount cleanup: no transition may wait on a vanished host. */
export function mailDiscardConfirmDialogHostUnmounted(): void {
  if (!open && pendingResolvers.length === 0) return
  resolveMailDiscardConfirmDialog(false)
}
