// ApprovalCard — the choices row of every approval card (Skills & Tools
// C-12, C-24, C-26; founder R2-Q1).
//
// The server computes the actions a card may offer (`offered_actions`) and
// the time choices that go with them (`allow_durations`); the card renders
// exactly those and nothing else. No card offers a lasting allow: a lasting
// Allow exists only as stored policy (Skills & Tools, or an agent's Tools
// tab). The five exact-action tools and the always-ask tools never get a time
// choice, so a person always sees the exact action.
//
// `ApprovalChoices` is the shared row the tool-approval panel and modal
// render. `ApprovalCard` is the skill card's decision row: it maps the C-12
// decision-table inputs (which checks are open, whether the skill is
// verified) onto the same offered actions the server computes for them.

import { useId, type Ref } from 'react'
import { CheckCircle, Clock, SealCheck, XCircle } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { isAlwaysAskTool } from '@/lib/alwaysAskTools'

export type ApprovalAction = ToolApprovalRequiredFrame['offered_actions'][number]
export type ApprovalDuration = ToolApprovalRequiredFrame['allow_durations'][number]

/**
 * C-26 point 9 (founder Q5 A): these tools keep a card for every action and
 * are never covered by a time limit. Mirrors the server's ExactActionTools.
 */
export const EXACT_ACTION_TOOLS: readonly string[] = [
  'environment_setup',
  'request_mount',
  'send_email',
  'browser_evaluate',
  'set_config',
]

export const ALL_DURATIONS: readonly ApprovalDuration[] = ['1h', '3h', '1d']

const DURATION_WORDS: Record<ApprovalDuration, string> = {
  '1h': '1 hour',
  '3h': '3 hours',
  '1d': '1 day',
}

export function durationWords(duration: ApprovalDuration): string {
  return DURATION_WORDS[duration]
}

/**
 * The actions a card offers. The server's list wins; an entry without one
 * (built before the server sent it) follows the tool's own rule: exact-action
 * and always-ask tools answer one request at a time, every other tool may be
 * allowed for a limited time.
 */
export function resolveOfferedActions(
  toolName: string,
  offered: readonly ApprovalAction[] | undefined,
): readonly ApprovalAction[] {
  if (offered && offered.length > 0) return offered
  if (EXACT_ACTION_TOOLS.includes(toolName) || isAlwaysAskTool(toolName)) {
    return ['allow_once', 'deny', 'cancel']
  }
  return ['allow_once', 'allow_for', 'deny', 'cancel']
}

/** The time choices shown with a timed action; empty when none is offered. */
export function resolveDurations(
  offered: readonly ApprovalAction[],
  durations: readonly ApprovalDuration[] | undefined,
): readonly ApprovalDuration[] {
  const timed = offered.includes('allow_for') || offered.includes('approve_version_allow_for')
  if (!timed) return []
  return durations && durations.length > 0 ? durations : ALL_DURATIONS
}

export function coversSentence(agentName: string): string {
  return `Covers ${agentName} in all its chats, tasks and its own helpers until the time ends or Omnipus restarts. Other agents still ask.`
}

export interface ApprovalChoice {
  action: ApprovalAction
  duration?: ApprovalDuration
}

interface ApprovalChoicesProps {
  offered: readonly ApprovalAction[]
  durations?: readonly ApprovalDuration[]
  agentName: string
  /** Label of the one-request approval (a readable preview may rename it). */
  onceLabel?: string
  /** Label of the deny action (a readable preview may rename it). */
  denyLabel?: string
  /** An unverified skill install under Ask: one install-and-approve choice. */
  installApproval?: boolean
  disabled?: boolean
  onChoose?: (choice: ApprovalChoice) => void
  /** The modal focuses Deny on open (the safe default). */
  denyRef?: Ref<HTMLButtonElement>
}

const BUTTON_CLASS = 'h-8 text-[length:var(--type-utility-xs-size)] flex-1 sm:flex-none'

/** The decision buttons of a card, exactly the offered actions (C-24). */
export function ApprovalChoices({
  offered,
  durations,
  agentName,
  onceLabel = 'Approve Once',
  denyLabel = 'Deny',
  installApproval = false,
  disabled = false,
  onChoose,
  denyRef,
}: ApprovalChoicesProps) {
  const scopeId = useId()
  const shownDurations = resolveDurations(offered, durations)
  const choose = (choice: ApprovalChoice) => onChoose?.(choice)
  const timedAction: ApprovalAction | undefined = offered.includes('approve_version_allow_for')
    ? 'approve_version_allow_for'
    : offered.includes('allow_for')
      ? 'allow_for'
      : undefined
  const timedPrefix = timedAction === 'approve_version_allow_for' ? 'Approve this version and allow for' : 'Allow for'

  return (
    <>
      {offered.includes('approve_version') && (
        <Button
          size="sm"
          variant="default"
          disabled={disabled}
          onClick={() => choose({ action: 'approve_version' })}
          className={BUTTON_CLASS}
        >
          <SealCheck size={14} weight="bold" aria-hidden="true" />
          Approve this version
        </Button>
      )}
      {offered.includes('allow_once') && (
        <Button
          size="sm"
          variant="default"
          disabled={disabled}
          onClick={() => choose({ action: 'allow_once' })}
          className={BUTTON_CLASS}
        >
          <CheckCircle size={14} weight="bold" aria-hidden="true" />
          {installApproval && !timedAction ? 'Install and approve this version' : onceLabel}
        </Button>
      )}
      {timedAction && shownDurations.length > 0 && (
        <div
          role="group"
          aria-label={`Allow ${agentName} for a limited time`}
          aria-describedby={scopeId}
          className="flex w-full flex-col gap-[var(--space-1)]"
        >
          <div className="flex flex-wrap gap-[var(--space-2)]">
            {shownDurations.map((duration) => (
              <Button
                key={duration}
                size="sm"
                variant="outline"
                disabled={disabled}
                onClick={() => choose({ action: timedAction, duration })}
                className={BUTTON_CLASS}
              >
                <Clock size={14} aria-hidden="true" />
                {`${timedPrefix} ${durationWords(duration)}`}
              </Button>
            ))}
          </div>
          <p id={scopeId} className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            {coversSentence(agentName)}
          </p>
        </div>
      )}
      {offered.includes('deny') && (
        <Button
          ref={denyRef}
          size="sm"
          variant="outline"
          disabled={disabled}
          onClick={() => choose({ action: 'deny' })}
          className={BUTTON_CLASS}
        >
          <XCircle size={14} weight="bold" aria-hidden="true" />
          {denyLabel}
        </Button>
      )}
    </>
  )
}

/**
 * The C-12 decision-table inputs of a skill card (a skill load or an
 * install_skill call). The server sends the same facts as
 * ToolApprovalRequiredFrame.skill.open_checks; this projection keeps the card
 * renderable from either. // not-wire-format
 */
export interface SkillApprovalEntry {
  toolName: string
  skillName?: string
  agentName?: string
  verified?: boolean
  trustOpen?: boolean
  permissionOpen?: boolean
  disabled?: boolean
  onChoose?: (choice: ApprovalChoice) => void
}

/**
 * The actions of a skill card by the C-12 decision table: permission open →
 * Approve Once, the time choices, Deny; trust open → Approve this version,
 * Deny; both → Approve this version, Approve this version and allow for …,
 * Deny; an unverified install under Ask → one install-and-approve choice and
 * Deny.
 */
export function skillCardActions(entry: Pick<SkillApprovalEntry, 'toolName' | 'trustOpen' | 'permissionOpen' | 'verified'>): {
  offered: ApprovalAction[]
  installApproval: boolean
} {
  const install = entry.toolName === 'install_skill'
  if (install && entry.trustOpen && entry.verified === false) {
    return { offered: ['allow_once', 'deny'], installApproval: true }
  }
  if (!install && entry.trustOpen && entry.permissionOpen) {
    return { offered: ['approve_version', 'approve_version_allow_for', 'deny'], installApproval: false }
  }
  if (!install && entry.trustOpen) {
    return { offered: ['approve_version', 'deny'], installApproval: false }
  }
  return { offered: ['allow_once', 'allow_for', 'deny'], installApproval: false }
}

/** The decision row of a skill approval card (C-12 rows 2–7). */
export function ApprovalCard({ entry }: { entry: SkillApprovalEntry }) {
  const { offered, installApproval } = skillCardActions(entry)
  return (
    <div className="flex flex-wrap gap-[var(--space-2)]" data-testid="skill-approval-choices">
      <ApprovalChoices
        offered={offered}
        agentName={entry.agentName ?? 'this agent'}
        installApproval={installApproval}
        disabled={entry.disabled}
        onChoose={entry.onChoose}
      />
    </div>
  )
}

function twoDigits(n: number): string {
  return String(n).padStart(2, '0')
}

/** "14:05", or "tomorrow 13:05" when the end is not today (S-126). */
export function formatAllowanceEnd(iso: string, now: Date = new Date()): string {
  const end = new Date(iso)
  const time = `${twoDigits(end.getHours())}:${twoDigits(end.getMinutes())}`
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  if (sameDay(end, now)) return time
  const tomorrow = new Date(now)
  tomorrow.setDate(now.getDate() + 1)
  if (sameDay(end, tomorrow)) return `tomorrow ${time}`
  return `${end.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })} ${time}`
}

const NOT_APPLIED_PREFIX = 'Approved for this request only. The time limit was not saved'

/**
 * The result line after a timed choice (C-24, C-26): what the server stored,
 * never what was asked. A reply that does not confirm the allowance means
 * only this call was released.
 */
export function timedChoiceResult(
  resp: { applied?: { timed_allowance: boolean }; expires_at?: string; not_applied_reason?: string },
  agentName: string,
): { message: string; variant: 'success' | 'warning' } {
  if (resp.applied?.timed_allowance === true && resp.expires_at) {
    return { message: `Allowed for ${agentName} until ${formatAllowanceEnd(resp.expires_at)}.`, variant: 'success' }
  }
  const reason = resp.not_applied_reason?.trim()
  if (reason && reason.startsWith(NOT_APPLIED_PREFIX)) return { message: reason, variant: 'warning' }
  const tail = reason ? `: ${reason.replace(/\.$/, '')}.` : '.'
  return { message: `${NOT_APPLIED_PREFIX}${tail}`, variant: 'warning' }
}
