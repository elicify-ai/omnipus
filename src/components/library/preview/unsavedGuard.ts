// unsavedGuard.ts — cross-component "don't silently discard an unsaved Library
// edit" guard (library-spec.md's editing wiring: "Dirty-state guard: warn
// before navigating away from unsaved edits").
//
// Module-level singleton (not React state) by necessity: at most one Library
// file can be open for editing at a time (LibraryExplorer renders a single
// LibraryPreviewPane for its one `selectedEntry`), but the navigation actions
// that could discard that edit — clicking a different file row, a
// breadcrumb, "Show Hidden", the panel's own Close/Pop-out buttons — live in
// LibraryExplorer.tsx, a sibling file this task only mounts INTO rather than
// owns. A plain exported function LibraryExplorer's handlers call before
// mutating navigation state is the smallest correct seam between the two:
// no prop-drilling a dirty flag up through every handler, no lifting editor
// state out of the component that actually owns it.
//
// useLibraryFileEditor (this directory) is the sole writer via
// setLibraryEditorDirty — one editor instance mounted at a time, so a plain
// boolean (not a Set/Map keyed by path) is correct; its own unmount effect
// always clears the flag, so a stale `true` can never outlive the editor that
// set it.

let dirty = false

/** Called by the active editor whenever its dirty state changes, and by its
 * unmount cleanup effect to clear the flag (see useLibraryFileEditor.ts). */
export function setLibraryEditorDirty(isDirty: boolean): void {
  dirty = isDirty
}

export function isLibraryEditorDirty(): boolean {
  return dirty
}

// ── In-app discard-confirmation dialog ─────────────────────────────────────
// A raw `window.confirm()` is a blocking browser dialog and a lock finding
// (controls/global-confirm, scripts/design-system-locks/controls.mjs) — the
// design system requires the catalogued `ConfirmDialog`
// (src/components/ui/confirm-dialog.tsx) instead. But `ConfirmDialog` is a
// React component, and this module is called from plain functions, not
// components, so it cannot render one itself. Instead it exposes a tiny
// external store (the same `subscribe`/`getSnapshot` shape
// src/lib/library-attachment.ts and LazyEmbedMount.tsx already use for a
// module-level value a React tree needs to read via `useSyncExternalStore`):
// flip `open` true, and let whichever `LibraryExplorer` instance is mounted
// render the dialog and report the user's choice back via
// `resolveDiscardConfirmDialog`. Both Library entry points (the docked panel
// via LibraryPanel.tsx, and the /library pop-out route) always keep a
// LibraryExplorer mounted for the whole time a navigation guard could fire —
// including the pop-out's `useBlocker`, which runs before the route (and so
// before LibraryExplorer) ever unmounts — so hosting the dialog inside
// LibraryExplorer covers every caller.
let open = false
const listeners = new Set<() => void>()
// FIFO of every caller currently waiting on the ONE dialog that is open.
// confirmDiscardLibraryEdits() can be called more than once before the user
// answers (e.g. a click storm, or one caller triggered programmatically while
// another is mid-click) — every one of them gets the SAME answer, from the
// same dialog, rather than stacking a second dialog per call.
let pendingResolvers: Array<(result: boolean) => void> = []

function emit(): void {
  for (const listener of listeners) listener()
}

export function subscribeDiscardConfirmDialog(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getDiscardConfirmDialogOpen(): boolean {
  return open
}

/**
 * Returns a Promise that resolves true if it's safe to proceed with
 * navigation (nothing unsaved, or the user confirmed discarding it) — and in
 * that case also clears the flag, since the caller is about to
 * unmount/replace whatever was dirty. Resolves false if the user chose to
 * stay (Cancel, Escape, or clicking outside the dialog).
 *
 * Every caller MUST `await` this — a Promise used directly in `!expr` is
 * always truthy, so an un-awaited call makes the guard permanently
 * a no-op with no compiler error.
 */
export function confirmDiscardLibraryEdits(): Promise<boolean> {
  if (!dirty) return Promise.resolve(true)
  const promise = new Promise<boolean>((resolve) => {
    pendingResolvers.push(resolve)
  })
  if (!open) {
    open = true
    emit()
  }
  return promise
}

/**
 * Called by the dialog host (LibraryExplorer) once the user answers —
 * Discard (`true`), or Cancel/Escape/outside-click (`false`). Answers every
 * pending `confirmDiscardLibraryEdits()` call with the same result.
 */
export function resolveDiscardConfirmDialog(result: boolean): void {
  if (result) dirty = false
  open = false
  const resolvers = pendingResolvers
  pendingResolvers = []
  emit()
  for (const resolve of resolvers) resolve(result)
}

// beforeunload (tab close / reload / browser back-forward-cache navigation):
// registered once at module load — this module is only ever imported by the
// Library preview/editor code path, so the listener existing is itself a
// no-op cost until an edit is actually made (guarded on `dirty` internally).
// This is NOT `window.confirm()` and stays exactly as it was: browsers only
// allow their own native prompt at this event, never an in-app dialog.
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', (e) => {
    if (!dirty) return
    e.preventDefault()
    // Chrome requires returnValue to be set (any string) to show the native
    // "leave site?" prompt; other browsers show a fixed message regardless.
    e.returnValue = ''
  })
}
