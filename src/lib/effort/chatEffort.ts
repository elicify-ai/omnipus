// chatEffort.ts — the chat's effort helpers (effort-levels-spec-r10.md,
// US-2, US-3 AS6, US-6 AS6, US-7; FR-023, FR-024/FR-036, FR-055, FR-058;
// S-12, S-21, S-36, S-41, S-43, S-95).
//
//   - chatInheritedEffort: what a Not set chat resolves to (the agent's
//     setting, or the default model's when the agent rides it).
//   - effortRecordNote: the per-turn note, a pure function of the stored
//     EffortRecord (one text per reason; never rebuilt from configuration).
//   - effortStatsLine / thinkingStatsLine: the turn statistics tooltip lines.
//   - decideWebEffort: the web composer's `/effort` (delivery client), the
//     same replies the messenger command gives (pkg/commands/cmd_effort.go).

import type { EffortRecord } from '@/lib/api/generated/asyncapi-types'
import { effortLabel, effortTurnNote, PROVIDER_DEFAULT } from './mapEffort'
import { effortTargetOf, type ServedRow } from './servedReasoning'

/** The value a Not set chat inherits: the agent's own setting; when the agent rides the default model, the default model's. */
export function chatInheritedEffort(
  agent: { model?: string | null; reasoning_effort?: string | null } | undefined,
  defaultModelEffort: string | undefined,
): string {
  const own = agent?.reasoning_effort ?? ''
  if (own !== '') return own
  const ridesDefault = (agent?.model ?? '').trim() === ''
  return ridesDefault ? (defaultModelEffort ?? '') : ''
}

/**
 * The note under an answer: shown only when what was sent differs from what
 * was chosen (a `reason` is present). Returns null when no note is due.
 */
export function effortRecordNote(record: EffortRecord | undefined): string | null {
  if (!record?.reason || !record.chosen) return null
  const model = record.model ?? ''
  const provider = record.provider ?? ''
  switch (record.reason) {
    case 'mapped':
      // Empty candidate (r7): the stored value kept, nothing sent.
      return record.sent ? effortTurnNote('mapped', model, record.chosen, record.sent) : effortTurnNote('empty_candidate', model, record.chosen)
    case 'not_supported':
      return effortTurnNote('not_supported', model, record.chosen)
    case 'continuation':
      return `Effort not sent: ${model} was continuing a tool call without its own reasoning.`
    case 'rejected':
      return `${provider} rejected the effort setting for ${model}. Sent without effort.`
    case 'rejected_earlier':
      return `Effort not sent: ${provider} rejected it earlier.`
    case 'extra_body':
      return `Your config's extra_body sent reasoning_effort=${record.sent ?? ''} instead.`
  }
  // The contract's `provider_default` reason is never written: a Provider
  // default choice is stored without a reason (pkg/agent/effort_request.go::record),
  // and a model with nothing sendable is recorded as `not_supported`. No note.
  return null
}

/** "Effort: High" — what the request carried; null when the turn has no effort record. */
export function effortStatsLine(record: EffortRecord | undefined): string | null {
  if (!record?.chosen) return null
  if (record.sent) return `Effort: ${effortLabel(record.sent)}`
  if (record.chosen === PROVIDER_DEFAULT && !record.reason) return 'Effort: Provider default'
  return 'Effort: not sent'
}

/** "Thinking: 120 tokens (reported by the provider)" or "Thinking: not reported" (absent = not reported, 0 = reported zero). */
export function thinkingStatsLine(tokens: number | undefined): string {
  if (tokens === undefined) return 'Thinking: not reported'
  return `Thinking: ${tokens.toLocaleString('en-US')} ${tokens === 1 ? 'token' : 'tokens'} (reported by the provider)`
}

// ── /effort on the web (FR-023, S-36, S-41) ─────────────────────────────────

export interface WebEffortState {
  /** The chat's model (the composer's current pair). */
  model: string
  /** Its served catalog row. */
  row: ServedRow
  /** The conversation's own value; '' = Not set. */
  current: string
  /** What Not set inherits (the agent's setting). */
  agentSetting: string
}

export interface WebEffortDecision {
  reply: string
  /** Set when the conversation's value changes. */
  change?: { value: string }
}

const EFFORT_MAX_CHARS = 32
const RANKED_NAMES = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']
const INVALID = "That isn't a valid effort level."

function settingLabel(value: string): string {
  return value === '' ? 'Not set' : effortLabel(value)
}

function optionsText(levels: readonly string[], hasSwitch: boolean): string {
  const options = ['Provider default']
  if (levels.includes('none') || hasSwitch) options.push('Off')
  for (const level of levels) if (level !== 'none') options.push(level)
  if (hasSwitch) options.push('On')
  return options.join(', ')
}

/** One `/effort` argument against the chat's model: the reply and, when valid, the new value. */
export function decideWebEffort(state: WebEffortState, rawArgument: string): WebEffortDecision {
  const argument = rawArgument.trim().toLowerCase()
  const { model, row } = state
  const target = effortTargetOf(row)
  const sendable = target.levels.length > 0 || target.switch
  if (argument === '') {
    const current = settingLabel(state.current !== '' ? state.current : state.agentSetting)
    const options = row.found && target.reasons && sendable ? ` Options: ${optionsText(target.levels, target.switch)}.` : ''
    return { reply: `Effort for ${model}: ${current}.${options} /effort reset returns to the agent's setting.` }
  }
  if ([...argument].length > EFFORT_MAX_CHARS || /\s/.test(argument)) return { reply: INVALID }
  if (argument === 'default' || argument === PROVIDER_DEFAULT) {
    return { reply: `Effort for ${model}: Provider default (the provider decides).`, change: { value: PROVIDER_DEFAULT } }
  }
  if (argument === 'reset') {
    return { reply: `Effort for ${model}: back to the agent's setting (${settingLabel(state.agentSetting)}).`, change: { value: '' } }
  }
  if (!row.found) return { reply: `Effort for ${model}: ${settingLabel(argument)}.`, change: { value: argument } }
  if (!target.reasons) return { reply: `${model} doesn't support reasoning effort.` }
  if (!sendable) return { reply: `Omnipus can't change effort for ${model}.` }
  const unoffered = `${model} doesn't offer ${argument}. Options: ${optionsText(target.levels, target.switch)}. Nothing changed.`
  if (argument === 'off' || argument === 'none') {
    if (target.levels.includes('none') || target.switch) return { reply: `Effort for ${model}: Off.`, change: { value: 'off' } }
    return { reply: `${model} can't turn reasoning off.` }
  }
  if (argument === 'on') {
    return target.switch ? { reply: `Effort for ${model}: On.`, change: { value: 'on' } } : { reply: unoffered }
  }
  if (!RANKED_NAMES.includes(argument) && !target.levels.includes(argument)) return { reply: INVALID }
  if (target.levels.length === 0) {
    return { reply: `${model} only switches reasoning on or off. Use /effort on, /effort off or /effort default.` }
  }
  if (target.levels.includes(argument)) return { reply: `Effort for ${model}: ${effortLabel(argument)}.`, change: { value: argument } }
  return { reply: unoffered }
}
