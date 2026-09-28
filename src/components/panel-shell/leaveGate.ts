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
// confirmation. The registry also supplies its optional synchronous
// `beforeLeaveRequired` predicate:
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
// Waves 2/3 add their guards through the same definition; this helper has no
// panel-specific branch.

import { getPanelDefinition } from './registry'
import type { PanelId } from './types'

/**
 * Run the outgoing panel's leave gate, then `go()` if it allows the
 * transition. Synchronous on the clean path (see module comment); async
 * dialog round-trip when the outgoing Library has unsaved edits.
 */
export function leaveGateThen(outgoingPanelId: PanelId | null, go: () => void): void {
  const definition = outgoingPanelId === null ? undefined : getPanelDefinition(outgoingPanelId)
  const guard = definition?.beforeLeave
  if (guard === undefined || definition?.beforeLeaveRequired?.() === false) {
    go()
    return
  }
  void (async () => {
    if (await guard()) go()
  })()
}
