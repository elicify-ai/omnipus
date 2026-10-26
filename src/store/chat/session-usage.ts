/** Merge recorded absolute usage for one addressed canonical session only.
 * Callers retain their existing view/owner/cursor fences. Missing is unknown,
 * including on a fresh bucket; neither replay diagnostics nor turn deltas are
 * an aggregate baseline. No page/body retention or independent cache lives here.
 */
export function mergeRecordedSessionTokens(current: number | undefined, observed: number | undefined): number | undefined {
  if (observed === undefined || !Number.isInteger(observed) || observed < 0) return current
  return current === undefined ? observed : Math.max(current, observed)
}
