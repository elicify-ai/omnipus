// turn-liveness.ts — proves a second viewer attached WHILE a turn was live.
//
// CHECK 3 finding F1 (ciflake-check3-report.md): replay-fidelity (c) observed
// the composer Stop button before its 800ms gap and before the second
// viewer's navigation, and later accepted Stop's absence, so a turn that
// ended during the gap or the attach produced an ordinary completed-history
// replay and every assertion still passed. Contract: sprint-i-historical-
// replay-fidelity-spec.md Scenario 9 / TDD row 25 require active live
// production when the new browser attaches.
//
// The proof is a bracket: the Stop marker (visible exactly while the chat
// store holds isStreaming, OmnipusComposer.tsx:1017) must be visible
// immediately before the attach starts AND immediately after the attach
// completes. One turn mounts Stop once and unmounts it once, so a marker
// visible at both ends was live for the whole attach. Either end missing is
// a prerequisite failure that names which end — never a pass. Samples are
// instantaneous (isVisible does not wait), so this does not widen any wait.

import type { Locator } from '@playwright/test'

/** The one Locator method the bracket needs — injectable for unit tests. */
export type TurnMarker = Pick<Locator, 'isVisible'>

export const TURN_ENDED_BEFORE_ATTACH =
  'PREREQUISITE: the turn had already finished (composer Stop gone) before the second viewer started ' +
  'attaching, so the attach would be an ordinary completed-history replay, not an attach during live production.'

export const TURN_ENDED_DURING_ATTACH =
  "PREREQUISITE: the turn finished (composer Stop gone) before the second viewer's attach completed, so " +
  'the attach was not proven to overlap live production.'

/**
 * Runs `attach` with the turn marker sampled immediately before and after it.
 * Throws (and, for the "before" case, never starts `attach`) when the turn
 * was not live at either end; otherwise returns attach's result.
 */
export async function attachWhileTurnActive<T>(marker: TurnMarker, attach: () => Promise<T>): Promise<T> {
  if (!(await marker.isVisible())) throw new Error(TURN_ENDED_BEFORE_ATTACH)
  const result = await attach()
  if (!(await marker.isVisible())) throw new Error(TURN_ENDED_DURING_ATTACH)
  return result
}
