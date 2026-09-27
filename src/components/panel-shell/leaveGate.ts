// leaveGate.ts — the CRIT-001/FR-013 transition gate for app-initiated panel
// transitions that originate OUTSIDE the shell (ChatControls, the tab-strip
// toggle, the sidebar Library entry, the "Watch live" affordances). The shell
// itself gates through PanelDefinition.beforeLeave (usePanelShell::runGuard);
// these call sites run the same gate through this helper so EVERY path that
// closes or replaces a panel awaits the outgoing panel's leave confirmation
// first — "the guard runs before the store moves" (side-panel-shell-spec.md
// §7, CRIT-001).
//
// Wave 1 has exactly ONE guard in the registry — the Library's unsaved-edit
// confirmation (unsavedGuard.ts::confirmDiscardLibraryEdits). The gate is
// therefore the dirty-flag check itself:
//
//   - Clean (no unsaved Library edits — the flag can only be true while a
//     LibraryExplorer editor is mounted, i.e. while the Library panel is
//     open in THIS tab): `go()` runs SYNCHRONOUSLY. This is what makes the
//     tab-strip toggle's store update observable in the same tick as the
//     click (US-5's toggle tests assert synchronously) and keeps a clean
//     replace prompt-free (§5: "a CLEAN panel is replaced immediately, no
//     prompt").
//
//   - Dirty: the discard dialog opens (hosted by the mounted LibraryExplorer)
//     and only a Discard choice runs `go()` — the outgoing panel stays
//     mounted until the guard resolves, so Cancel leaves the store and URL
//     untouched.
//
// When waves 2/3 register panels with their own beforeLeave, this helper
// becomes registry-driven (look up the OUTGOING panel's definition and await
// its guard); the synchronous-clean-path shape stays.

import { isLibraryEditorDirty, confirmDiscardLibraryEdits } from '@/components/library/preview/unsavedGuard'

/**
 * Run the outgoing panel's leave gate, then `go()` if it allows the
 * transition. Synchronous on the clean path (see module comment); async
 * dialog round-trip when the outgoing Library has unsaved edits.
 */
export function leaveGateThen(go: () => void): void {
  if (!isLibraryEditorDirty()) {
    go()
    return
  }
  void confirmDiscardLibraryEdits().then((allowed) => {
    if (allowed) go()
  })
}
