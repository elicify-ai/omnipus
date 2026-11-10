// recapLimits.ts — the ten "Recap limits" settings of the Memory screen
// (effort-levels-spec-r10.md, L-table under FD-20; effort-recap-limits-addendum.md
// S1 to S6 for the help texts; S-82, S-89, S-93).
//
// One row per setting: its wire key, screen label, unit, range, shipped
// default and help text. The screen keeps every value as a draft string so a
// half-typed or out-of-range number stays exactly as typed (S-82), and checks
// the same ranges the server does before Save is allowed.

export type RecapLimitKey =
  | 'recap_max_output_tokens'
  | 'recap_input_tokens'
  | 'recap_summary_words'
  | 'recap_list_items'
  | 'recap_fallback_context_chars'
  | 'recap_timeout_seconds'
  | 'recap_stream_retries'
  | 'recap_retry_pause_ms'
  | 'recap_chars_per_token'
  | 'bootstrap_recap_min_spacing_seconds'

export type RecapLimitGroup = 'reads-writes' | 'timing'

export interface RecapLimitField {
  key: RecapLimitKey
  /** Screen label; also the subject of the range sentence. */
  label: string
  /** Unit shown after the label, e.g. "tokens". */
  unit?: string
  min: number
  max: number
  defaultValue: number
  group: RecapLimitGroup
}

export const RECAP_LIMIT_GROUP_TITLES: Record<RecapLimitGroup, string> = {
  'reads-writes': 'What the recap reads and writes',
  timing: 'Timing and retries',
}

export const RECAP_LIMIT_FIELDS: readonly RecapLimitField[] = [
  { key: 'recap_max_output_tokens', label: 'Answer length limit', unit: 'tokens', min: 512, max: 16384, defaultValue: 2048, group: 'reads-writes' },
  { key: 'recap_input_tokens', label: 'Conversation sent for the recap', unit: 'tokens', min: 500, max: 32000, defaultValue: 2000, group: 'reads-writes' },
  { key: 'recap_summary_words', label: 'Summary length', unit: 'words', min: 50, max: 500, defaultValue: 150, group: 'reads-writes' },
  { key: 'recap_list_items', label: 'Items per list', min: 1, max: 10, defaultValue: 5, group: 'reads-writes' },
  { key: 'recap_fallback_context_chars', label: 'Kept text if the recap fails', unit: 'characters', min: 200, max: 8000, defaultValue: 1500, group: 'reads-writes' },
  { key: 'recap_chars_per_token', label: 'Characters per token', unit: 'estimate', min: 2, max: 8, defaultValue: 4, group: 'reads-writes' },
  { key: 'recap_timeout_seconds', label: 'Recap time limit', unit: 'seconds', min: 15, max: 300, defaultValue: 60, group: 'timing' },
  { key: 'recap_stream_retries', label: 'Retries after a dropped connection', min: 0, max: 5, defaultValue: 2, group: 'timing' },
  { key: 'recap_retry_pause_ms', label: 'Pause before a retry', unit: 'milliseconds', min: 100, max: 5000, defaultValue: 500, group: 'timing' },
  { key: 'bootstrap_recap_min_spacing_seconds', label: 'Minimum gap between catch-up recaps', unit: 'seconds', min: 1, max: 60, defaultValue: 1, group: 'timing' },
]

/** The ChatGPT sign-in provider: its route takes no output limit from Omnipus (S-89, TD-2). */
export const CHATGPT_SIGN_IN_PROVIDER = 'openai-chatgpt'

export const CHATGPT_OUTPUT_LIMIT_TEXT =
  'Not applied to ChatGPT sign-in models: that route takes no output limit from Omnipus.'

/** Help text per setting. `charsPerToken` fills the L2 sentence with the current estimate. */
export function recapLimitHelp(key: RecapLimitKey, charsPerToken: string): string {
  switch (key) {
    case 'recap_max_output_tokens':
      return 'Most tokens the recap model may write, its reasoning included. Raise it if recaps come back cut off.'
    case 'recap_input_tokens':
      return `How much of the most recent user messages the recap reads, in tokens (about ${charsPerToken} characters each). Older messages are left out.`
    case 'recap_summary_words':
      return "Target length of the recap, in words. The recap is added to the agent's next conversation in this workspace, so longer costs more on every turn."
    case 'recap_list_items':
      return 'Most wins, improvements and things worth remembering the recap lists, for each of the three.'
    case 'recap_fallback_context_chars':
      return 'If the recap model fails, this many characters of your most recent messages are kept word for word so the next conversation still knows them.'
    case 'recap_timeout_seconds':
      return 'Longest a recap may take across its model and fallbacks, retries and pauses included, in seconds. Each model gets whatever time is left, so a model that hangs uses the rest and the fallbacks are not tried. A slow model may need more.'
    case 'recap_stream_retries':
      return 'Extra tries for one model when its connection drops before any answer arrives. 0 turns retries off.'
    case 'recap_retry_pause_ms':
      return 'Wait before the first retry; retry n waits n times this (default 0.5 s, then 1 s). A pause never runs past the recap time limit.'
    case 'recap_chars_per_token':
      return "Used to turn 'Conversation sent' into characters. Rarely needs changing."
    case 'bootstrap_recap_min_spacing_seconds':
      return 'Restart catch-up never starts two recaps closer together than this, whatever the rate.'
  }
}

export type RecapLimitDrafts = Record<RecapLimitKey, string>

/** The shipped defaults as drafts (Reset puts these back, S-82). */
export function defaultRecapLimitDrafts(): RecapLimitDrafts {
  const drafts = {} as RecapLimitDrafts
  for (const field of RECAP_LIMIT_FIELDS) drafts[field.key] = String(field.defaultValue)
  return drafts
}

/** The range sentence for an invalid draft, or null when the draft is a whole number in range. */
export function recapLimitError(field: RecapLimitField, draft: string): string | null {
  const trimmed = draft.trim()
  const value = /^\d+$/.test(trimmed) ? Number(trimmed) : Number.NaN
  if (Number.isInteger(value) && value >= field.min && value <= field.max) return null
  return `${field.label} must be between ${field.min} and ${field.max}`
}

/** How many displayed values differ from their shipped defaults (invalid drafts count). */
export function countChangedFromDefaults(drafts: RecapLimitDrafts): number {
  let changed = 0
  for (const field of RECAP_LIMIT_FIELDS) {
    const trimmed = drafts[field.key].trim()
    const value = /^\d+$/.test(trimmed) ? Number(trimmed) : Number.NaN
    if (value !== field.defaultValue) changed += 1
  }
  return changed
}

/** "all at defaults" or "N changed" (S-82). */
export function recapLimitsSummary(drafts: RecapLimitDrafts): string {
  const changed = countChangedFromDefaults(drafts)
  return changed === 0 ? 'all at defaults' : `${changed} changed`
}
