// truncation.ts — ADR-087 (Truncation is an outcome, not a silence), WP F.
//
// The backend persists two fields on the last assistant transcript entry of
// an incomplete turn: `truncated` (boolean, "this entry is incomplete") and
// `truncation_reason` (`'cancelled' | 'max_output_tokens'`, why). Every
// entry written before `truncation_reason` existed has `truncated: true`
// with no reason at all — ADR-087 §2.4/D2 pins the legacy default: an
// absent reason on a `truncated: true` entry means `'cancelled'` (the only
// way a turn could end incomplete before this ADR was a user cancel;
// `max_output_tokens` did not exist as a persisted concept until now).
//
// `normalizeTruncationReason` is the single place that legacy-default rule
// is applied, so the cold-load (REST `Message`) and WS-replay
// (`ReplayMessageFrame`) paths derive the same value from the same wire
// shape instead of re-implementing the rule twice and risking drift.
//
// This module is display/normalization-only — not-wire-format. The wire
// shapes it reads (`truncated?: boolean`, `truncation_reason?: 'cancelled' |
// 'max_output_tokens'`) are the generated `Message` / `ReplayMessageFrame`
// fields (src/lib/api/generated/*), per CLAUDE.md hard-constraint #8.

export type TruncationReason = 'cancelled' | 'max_output_tokens'

/**
 * Derive the effective truncation reason from the raw wire fields, applying
 * the ADR-087 D2 legacy default (absent reason on a truncated entry means
 * `'cancelled'`). Returns `undefined` when the entry isn't truncated at all.
 */
export function normalizeTruncationReason(
  truncated: boolean | undefined,
  reason: TruncationReason | undefined,
): TruncationReason | undefined {
  if (!truncated) return undefined
  return reason ?? 'cancelled'
}

export interface StatusSuffixInput { // not-wire-format: render-layer pick of ChatMessage fields consumed by getMessageStatusSuffix
  role?: string
  status?: string
  truncationReason?: TruncationReason
}

/** Exact suffix text D1 requires for a cancelled/interrupted turn. */
export const INTERRUPTED_SUFFIX_TEXT = '(interrupted)'
/** Exact suffix text D1 requires for a provider output-token-limit cutoff. */
export const CUT_OFF_SUFFIX_TEXT = '(cut off at the output limit)'

/**
 * ADR-087 D1 — the muted-footer status suffix for an assistant message, or
 * `null` when neither applies.
 *
 * Precedence is fixed and load-bearing: an interrupted/cancelled turn ALWAYS
 * wins over an output-token-limit cutoff — render exactly one suffix, never
 * both. `status === 'interrupted'` is the pre-existing (FR-21) signal;
 * `truncationReason === 'cancelled'` is the new ADR-087 signal for the same
 * outcome arriving via the `truncated`/`truncation_reason` fields instead of
 * (or in addition to) `status`. Either one alone is sufficient.
 */
export function getMessageStatusSuffix(message: StatusSuffixInput): string | null {
  if (message.role !== 'assistant') return null
  if (message.status === 'interrupted' || message.truncationReason === 'cancelled') {
    return INTERRUPTED_SUFFIX_TEXT
  }
  if (message.truncationReason === 'max_output_tokens') {
    return CUT_OFF_SUFFIX_TEXT
  }
  return null
}
