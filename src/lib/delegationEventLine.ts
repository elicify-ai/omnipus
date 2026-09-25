/**
 * Sentence and [open] rule for one delegation event line.
 * Words match docs/internal/specs/delegation-chat-surface-spec.md.
 * The leading mark (arrow, check, warning, stop) is a Phosphor icon in the
 * component — UI chrome does not carry emoji glyphs.
 */
import type { DelegationEvent, DelegationEventKind } from './delegationEvents.types'

const SUBAGENT_KINDS = new Set<DelegationEventKind>([
  'delegated',
  'started',
  'finished',
  'stopped',
  'steered',
  'answered',
  'cancelled',
  'follow_up',
])

function named(value: string | undefined, fallback: string): string {
  const trimmed = value?.trim()
  return trimmed ? trimmed : fallback
}

function withTitle(sentence: string, title: string | undefined): string {
  const trimmed = title?.trim()
  return trimmed ? `${sentence} · ${trimmed}` : sentence
}

/** The words of the line, without the leading mark. */
export function delegationEventLineText(event: DelegationEvent): string {
  const agent = named(event.agentName, 'an agent')
  switch (event.kind) {
    case 'delegated':
      return withTitle(`Delegated to ${agent}`, event.title)
    case 'started':
      return withTitle(`${agent} started`, event.title)
    case 'finished':
      return withTitle(`${agent} finished`, event.title)
    case 'stopped':
      return `${agent} stopped without finishing`
    case 'steered':
      return `Sent ${agent} a new instruction`
    case 'answered':
      return `Answered ${agent}'s question`
    case 'cancelled':
      // Founder decision 2026-09-25: no "and N below it" — the SPA never receives
      // the cascade count, and the panel already lists every stopped child.
      return `Stopped ${agent}`
    case 'follow_up':
      return withTitle(`Gave ${agent} follow-up work`, event.title)
    case 'refused':
      return withTitle('Delegation refused', event.reason)
    case 'bash_launched':
      return withTitle('Running in background', event.command)
    case 'bash_finished':
      return `${named(event.command, 'command')} finished`
    case 'bash_failed': {
      const command = named(event.command, 'command')
      return typeof event.exitCode === 'number' ? `${command} failed (exit ${event.exitCode})` : `${command} failed`
    }
    case 'bash_stopped':
      return `Stopped ${named(event.command, 'command')}`
    default: {
      const neverKind: never = event.kind
      return neverKind
    }
  }
}

/**
 * [open] only on a subagent line that names a child session.
 * Refusals and background commands never get one, even if a child id leaked in.
 */
export function delegationEventShowsOpen(event: DelegationEvent): boolean {
  return SUBAGENT_KINDS.has(event.kind) && typeof event.childSessionId === 'string' && event.childSessionId.length > 0
}
