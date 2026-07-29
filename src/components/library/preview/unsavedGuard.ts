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

/**
 * Returns true if it's safe to proceed with navigation (nothing unsaved, or
 * the user confirmed discarding it) — and in that case also clears the flag,
 * since the caller is about to unmount/replace whatever was dirty. Returns
 * false if the user chose to stay.
 */
export function confirmDiscardLibraryEdits(): boolean {
  if (!dirty) return true
  const proceed = window.confirm(
    'You have unsaved changes in the Library editor. Leaving now will discard them. Continue?',
  )
  if (proceed) dirty = false
  return proceed
}

// beforeunload (tab close / reload / browser back-forward-cache navigation):
// registered once at module load — this module is only ever imported by the
// Library preview/editor code path, so the listener existing is itself a
// no-op cost until an edit is actually made (guarded on `dirty` internally).
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', (e) => {
    if (!dirty) return
    e.preventDefault()
    // Chrome requires returnValue to be set (any string) to show the native
    // "leave site?" prompt; other browsers show a fixed message regardless.
    e.returnValue = ''
  })
}
