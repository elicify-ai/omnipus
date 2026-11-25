// ApprovalCard — the choices row of every approval card (Skills & Tools
// C-12, C-24, C-26; founder R2-Q1).
//
// The server computes the actions a card may offer (`offered_actions`) and
// the time choices that go with them (`allow_durations`); the card renders
// exactly those and nothing else. No card offers a lasting allow: a lasting
// Allow exists only as stored policy (Skills & Tools, or an agent's Tools
// tab). The server gives the five exact-action tools and the always-ask tools
// no time choice, so a person always sees the exact action.
//
// `ApprovalChoices` is the shared row the tool-approval panel and modal
// render. `ApprovalCard` is the skill card's decision row: it renders the
// lists the server computed for the skill card.

import { useId, type Ref } from 'react'
import { CheckCircle, Clock, SealCheck, XCircle } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { parseServerErrorField } from '@/lib/api-error'

export type ApprovalAction = ToolApprovalRequiredFrame['offered_actions'][number]
export type ApprovalDuration = ToolApprovalRequiredFrame['allow_durations'][number]

const DURATION_WORDS: Record<ApprovalDuration, string> = {
  '1h': '1 hour',
  '3h': '3 hours',
  '1d': '1 day',
}

export function durationWords(duration: ApprovalDuration): string {
  return DURATION_WORDS[duration]
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
  /** The server's time choices; shown only with a timed action. */
  durations: readonly ApprovalDuration[]
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
  const choose = (choice: ApprovalChoice) => onChoose?.(choice)
  const timedAction: ApprovalAction | undefined = offered.includes('approve_version_allow_for')
    ? 'approve_version_allow_for'
    : offered.includes('allow_for')
      ? 'allow_for'
      : undefined
  const shownDurations = timedAction ? durations : []
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
 * A skill card's entry (a skill load or an install_skill call): the C-12
 * facts plus the two lists the server computed for the card. The card shows
 * exactly those lists. // not-wire-format
 */
export interface SkillApprovalEntry {
  toolName: string
  skillName?: string
  agentName?: string
  verified?: boolean
  trustOpen?: boolean
  permissionOpen?: boolean
  offeredActions: readonly ApprovalAction[]
  allowDurations: readonly ApprovalDuration[]
  disabled?: boolean
  onChoose?: (choice: ApprovalChoice) => void
}

/** The decision row of a skill approval card (C-12 rows 2–7). */
export function ApprovalCard({ entry }: { entry: SkillApprovalEntry }) {
  // C-12 row 6: an unverified install under Ask words its one approval as
  // "Install and approve this version".
  const installApproval = entry.toolName === 'install_skill' && entry.verified === false && entry.trustOpen === true
  return (
    <div className="flex flex-wrap gap-[var(--space-2)]" data-testid="skill-approval-choices">
      <ApprovalChoices
        offered={entry.offeredActions}
        durations={entry.allowDurations}
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

/** C-24: the card's own words for a refused answer (400). */
export const CHOICE_NOT_OFFERED = 'This choice is no longer offered. Choose Approve Once or a time limit.'
/** C-24: a 500 stored nothing; the card stays answerable. */
export const APPROVAL_NOT_SAVED = 'Could not save the approval. Nothing was changed.'

/**
 * What the card says when the server refused or could not store an answer
 * (spec "Approval card" Error cells, C-24): a 409 carries the server's own
 * reason ("The skill changed after this request." and the like), a 400 means
 * the choice is not offered, a 500 means nothing was stored. Undefined for
 * any other status, or a 409 without a reason, so the caller keeps its
 * general wording.
 */
export function refusedAnswerMessage(status: number, body: string | undefined): string | undefined {
  if (status === 400) return CHOICE_NOT_OFFERED
  if (status === 409) return parseServerErrorField(body)
  if (status === 500) return APPROVAL_NOT_SAVED
  return undefined
}

/**
 * The result line of a stored version approval (C-24): said only when the
 * server reports a trust approval, never for Approve Once or a time choice.
 */
export function versionApprovedResult(
  resp: { applied?: { trust_approved?: boolean } },
  skillName: string | undefined,
): { message: string; variant: 'success' } | undefined {
  if (resp.applied?.trust_approved !== true || !skillName) return undefined
  return { message: `Approved this version of ${skillName}.`, variant: 'success' }
}

const NOT_APPLIED_PREFIX ='Approved for this request only. The time limit was not saved'

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
