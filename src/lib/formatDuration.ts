/**
 * Human-readable duration: "0ms" / "420ms" / "1.3s". Shared by every tool-call
 * / subagent-span status indicator (SubagentBlock, ActivityPanel,
 * ToolCallBadge, GenericToolCall) — previously reimplemented 4x independently
 * with inconsistent zero-duration behavior (2 sites returned "0ms" for a
 * zero/near-zero duration, 2 returned "").
 *
 * Canonical behavior: a genuine (non-null/undefined) duration always renders,
 * including exactly 0ms — "0ms" is more informative than an empty string for
 * a real (if instant) measured duration. Only a missing value renders as "".
 */
export function formatDuration(ms?: number): string {
  if (ms == null) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}
