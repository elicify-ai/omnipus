// ActivityPanel — slide-out detail view for the Activity Bar (Sheet-based,
// mirroring the Sheet/SheetContent/SheetHeader/SheetTitle usage pattern
// SessionPanel.tsx once used — since-deleted; session-list UI now lives in
// SearchModal + the sidebar accordion).
//
// Two sections — "Running now" / "Recently finished" — each hidden entirely
// when empty. Rows are flat text-lines (ticket "Tool components in chat",
// P2 — delegate-card restyle): no bordered/filled card, no status pill — an
// 8px status dot (or the spinning icon while running) + muted status text,
// via the shared getSpanStatusDot helper (src/lib/toolStatusConfig.tsx).
// Two things intentionally differ here vs SubagentBlock, expressed as
// options rather than a separate reimplementation: icon size 12 (vs 13), and
// the "running" label reads "running" here vs "working" there, since a row
// can be a bash call as well as an agent span. Expanded native-agent rows
// use the same indented border-l-2 accent line as SubagentBlock/ToolCallBadge
// instead of the old bordered/backgrounded panel.
//
// ── ADR-053 FE-5 (design §4 H-1..H-6): the open/close `subagent_start`/
// `subagent_end` span brackets GROW INTO A LIVE SESSION LIST. When an agent
// span carries mid-span enrichment (useRunningActivity joins the
// sessionActivity store's `subagent_message` / `subagent_state` frames by
// span_id), its expanded row shows the child session's durable 8-state
// lifecycle badge + its latest typed child→parent messages (progress /
// checkpoint / artifact / blocker / question / decision_request / handback),
// rendered through <UntrustedChildText> (FE-7 sanitization). PEEK / REPLY /
// STEER / STOP affordances are gated on the human owning the session
// (`ownsSession`) and dispatch through `onSessionAction`. PEEK toggles the
// inline message list locally; the others delegate to the host
// (ActivityBar), which wires stop → cancelStream and reply/steer → the
// conversational-chat path (FE-3: the human replies in normal chat, no
// per-question reply card).

import { useState } from 'react'
import {
  CaretDown,
  CaretUp,
  Check,
  X,
  Eye,
  ArrowBendUpLeft,
  Compass,
  StopCircle,
} from '@phosphor-icons/react'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { ActivityAvatar } from './ActivityAvatar'
import { ToolCallBadge } from './ToolCallBadge'
import { UntrustedChildText } from './UntrustedChildText'
import type { ActivityItem } from '@/hooks/useRunningActivity'
import type { SessionMessageRow, SessionLifecycleState } from '@/store/sessionActivity'
import { cn } from '@/lib/utils'
import { formatDuration } from '@/lib/formatDuration'
import { getSpanStatusDot, statusDot } from '@/lib/toolStatusConfig'
import { formatInterruptReason } from '@/lib/subagentStatus'

/**
 * The action a session-list affordance dispatches. PEEK is handled inline
 * (toggles the message list) and is NOT propagated. REPLY/STEER route to the
 * conversational-chat path (FE-3); STOP sends a cancel. The host wires the
 * concrete behavior.
 */
export type SessionAction = 'peek' | 'reply' | 'steer' | 'stop'

export interface SessionActionTarget {
  spanId: string
  /** Child durable session id, when the lifecycle state frame carried one. */
  sessionId?: string
  /**
   * Correlation id of the latest open question/decision_request from the
   * child, when one is pending — REPLY threads its answer through it.
   */
  correlationId?: string
}

export interface ActivityPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  running: ActivityItem[]
  recentlyFinished: ActivityItem[]
  /**
   * FE-5 affordance dispatch (peek/reply/steer/stop). Optional — when
   * omitted, PEEK still works (inline toggle); the other three render as
   * disabled-with-tooltip. Wired by ActivityBar.
   */
  onSessionAction?: (action: SessionAction, target: SessionActionTarget) => void
}

/** Label + tone for each of the 8 durable lifecycle states (SessionLifecycleRecord). */
function lifecycleBadge(state: SessionLifecycleState): { label: string; className: string } {
  switch (state) {
    case 'queued':
      return { label: 'queued', className: 'text-[var(--color-muted)]' }
    case 'running':
      return { label: 'running', className: 'text-[var(--color-accent)]' }
    case 'needs_input':
      return { label: 'needs input', className: 'text-[var(--color-warning,#D4AF37)]' }
    case 'paused':
      return { label: 'paused', className: 'text-[var(--color-muted)]' }
    case 'completed':
      return { label: 'done', className: 'text-[var(--color-success)]' }
    case 'failed':
      return { label: 'failed', className: 'text-[var(--color-error)]' }
    case 'cancelled':
      return { label: 'cancelled', className: 'text-[var(--color-muted)]' }
    case 'timed_out':
      return { label: 'timed out', className: 'text-[var(--color-error)]' }
    default:
      return { label: state, className: 'text-[var(--color-muted)]' }
  }
}

/** Compact label for a mid-span message kind (progress/checkpoint/…). */
function messageKindLabel(kind: SessionMessageRow['kind']): string {
  switch (kind) {
    case 'progress':
      return 'progress'
    case 'checkpoint':
      return 'checkpoint'
    case 'artifact':
      return 'artifact'
    case 'blocker':
      return 'blocker'
    case 'question':
      return 'question'
    case 'decision_request':
      return 'decision'
    case 'error':
      return 'error'
    case 'handback':
      return 'handback'
    case 'steer':
      return 'steer'
    case 'respond':
      return 'respond'
    default:
      return kind
  }
}

function ActivityRow({
  item,
  onSessionAction,
}: {
  item: ActivityItem
  onSessionAction?: (action: SessionAction, target: SessionActionTarget) => void
}) {
  const [expanded, setExpanded] = useState(false)
  // FE-5: PEEK toggles the inline session message list independently of the
  // row's expand (which shows tool-call steps / final result). A user can
  // peek the live child messages without scrolling the full step list.
  const [peekOpen, setPeekOpen] = useState(false)
  const config = getSpanStatusDot(item.status, { size: 12, runningLabel: 'running' })
  // JudgeActivityItem carries no durationMs (no wire-level "judge started"
  // moment to measure elapsed time from — see its doc comment).
  const duration = item.kind === 'judge' ? '' : formatDuration(item.durationMs)
  const label = item.kind === 'bash' ? item.command : item.kind === 'judge' ? `Judge · ${item.scope} round ${item.round}` : item.taskLabel
  // Narrowed inline (not via a stored boolean) so `steps` stays typed without a cast.
  const steps = item.kind === 'agent' && item.agentType !== '3p' ? item.steps : null
  const show3pNotice = item.kind === 'agent' && item.agentType === '3p'
  // Fix 2 (2026-07-16): the panel is now the durable surface for the final
  // result / interrupt reason SubagentBlock's (now thread-hidden-by-default)
  // card used to carry — see useRunningActivity.ts's AgentActivityItem.
  const finalResult = item.kind === 'agent' ? item.finalResult : undefined
  const interruptReason = item.kind === 'agent' ? item.interruptReason : undefined
  // ADR-049 D2/D4/US-13: a judge row is ALWAYS expandable — it has no
  // `steps` at all (there is no live "judge started" frame, only the
  // completed verdict push), but the per-criterion list is exactly the
  // "zero steps but a final result" case the BDD edge case calls out
  // ("Judge span with zero steps but a verdict — must stay expandable").
  const canExpandJudge = item.kind === 'judge'

  // ── FE-5 session-list enrichment (agent spans only) ──────────────────────
  // The mid-span sessionActivity data joined onto this item by useRunningActivity.
  // Declared ahead of `canExpand` below because a span with this enrichment
  // must be expandable even before it has any tool-call steps.
  const isAgent = item.kind === 'agent'
  const sessionMessages = isAgent ? item.sessionMessages : undefined
  const lifecycleState = isAgent ? item.lifecycleState : undefined
  const lifecycleSessionId = isAgent ? item.lifecycleSessionId : undefined
  const steeringReceipt = isAgent ? item.steeringReceipt : undefined
  const hasSessionDetail = isAgent && (sessionMessages != null || lifecycleState != null)

  // A span with zero steps but a final result (e.g. a very short delegation)
  // must still be expandable, or the result would never be reachable.
  // FE-5: a running agent span with mid-span session detail (lifecycle state
  // and/or child messages) but no steps yet must ALSO be expandable, or the
  // live session list would be unreachable until the first tool_call lands.
  const canExpand =
    canExpandJudge ||
    (steps != null && (steps.length > 0 || !!finalResult)) ||
    !!hasSessionDetail

  // Gated attach (FE-5): affordances only when the human owns the session.
  // v1: every span visible here belongs to the active chat session the human
  // is attached to, so the gate is open. When the full SessionLifecycleRecord
  // (owner_scope_kind) is surfaced, refine to `owner_scope_kind === 'human'`.
  const ownsSession = isAgent && item.status === 'running'
  const canPeek = hasSessionDetail && (sessionMessages?.length ?? 0) > 0
  // The latest pending question/decision_request correlation id (for REPLY).
  // Manual reverse search (Array.findLast needs es2023+ lib; the repo targets
  // earlier, so avoid it).
  let openCorrelationId: string | undefined
  if (sessionMessages) {
    for (let i = sessionMessages.length - 1; i >= 0; i--) {
      const m = sessionMessages[i]
      if ((m.kind === 'question' || m.kind === 'decision_request') && m.correlationId) {
        openCorrelationId = m.correlationId
        break
      }
    }
  }
  // REPLY needs either a pending question/decision OR a parent session to
  // steer; STEER/STOP need the session to be live. PEEK is independent.
  const actionTarget: SessionActionTarget = {
    spanId: item.key,
    sessionId: lifecycleSessionId,
    correlationId: openCorrelationId,
  }
  const affordancesWired = typeof onSessionAction === 'function'
  const canReply = ownsSession && openCorrelationId != null
  const canSteer = ownsSession && lifecycleSessionId != null
  const canStop = ownsSession && lifecycleSessionId != null

  return (
    <div
      data-testid="activity-row"
      data-status={item.status}
      className="text-xs"
    >
      {/* Mirrors GenericToolCall.tsx's `disabled={!hasDetail}` gate: a
          non-expandable row (bash calls, 3p-agent rows, or a native row with
          zero steps) has nothing to expand — disable it natively so it
          drops out of the tab order and Enter/Space can't no-op on it,
          rather than leaving a focusable dead button whose aria-expanded is
          already (correctly) omitted below. */}
      <button tabIndex={0}
        type="button"
        onClick={() => canExpand && setExpanded((e) => !e)}
        disabled={!canExpand}
        aria-expanded={canExpand ? expanded : undefined}
        className={cn(
          'flex w-full items-center gap-2 py-1.5 text-left transition-colors',
          canExpand ? 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer' : 'cursor-default',
        )}
      >
        <ActivityAvatar item={item} size="sm" />
        <span className="flex-1 min-w-0 truncate text-[var(--color-secondary)] font-medium font-mono">
          {label}
        </span>
        {config.indicator}
        <span className={cn('text-[var(--color-muted)] shrink-0', config.textClass)}>
          {config.label}
          {/* W1-9, carried via Fix 2: interrupt reason appended to the
              status text, matching SubagentBlock's own inline treatment. */}
          {item.status === 'interrupted' && interruptReason && (
            <span
              className="font-sans"
              title={`Interrupted: ${formatInterruptReason(interruptReason)}`}
            >
              {' '}({formatInterruptReason(interruptReason)})
            </span>
          )}
        </span>
        {duration && <span className="text-[var(--color-muted)] shrink-0 tabular-nums">{duration}</span>}
        {canExpand && (
          <span className="text-[var(--color-muted)] shrink-0">
            {expanded ? <CaretUp size={11} aria-hidden="true" /> : <CaretDown size={11} aria-hidden="true" />}
          </span>
        )}
      </button>

      {/* FE-5: live session-list detail strip (lifecycle badge + affordances).
          Rendered for any agent span that has mid-span enrichment, ABOVE the
          step list, when the row is expanded. The lifecycle badge is the
          durable 8-state authority; the affordances dispatch through
          onSessionAction (gated on ownsSession). PEEK toggles the inline
          message list below. */}
      {isAgent && expanded && hasSessionDetail && (
        <div className="ml-[3px] border-l-2 border-[var(--color-accent)]/50 pl-3 py-1 space-y-1.5" data-testid="session-list-detail">
          <div className="flex items-center gap-1.5 flex-wrap">
            {lifecycleState && (() => {
              const badge = lifecycleBadge(lifecycleState)
              return (
                <span
                  data-testid="session-lifecycle-badge"
                  data-lifecycle={lifecycleState}
                  className={cn(
                    'inline-flex items-center gap-1 px-1.5 py-px rounded text-[9px] font-mono uppercase tracking-wide bg-[var(--color-surface-2)] border border-[var(--color-border)]',
                    badge.className,
                  )}
                >
                  {lifecycleState === 'running' && statusDot('bg-[var(--color-accent)] animate-pulse')}
                  {badge.label}
                </span>
              )
            })()}
            {steeringReceipt && (
              <span
                className="inline-flex items-center gap-0.5 text-[9px] text-[var(--color-muted)] italic"
                title={`Steer applied ${steeringReceipt.appliedAt} (correlation ${steeringReceipt.correlationId})`}
              >
                <Check size={9} weight="bold" aria-hidden="true" /> steer applied
              </span>
            )}
            <span className="flex-1" />
            {/* Affordances — Peek / Reply / Steer / Stop (gated attach). */}
            <AffordanceButton
              testId="session-peek"
              icon={<Eye size={11} weight="regular" aria-hidden="true" />}
              label="Peek"
              active={peekOpen}
              disabled={!canPeek}
              disabledTitle={canPeek ? undefined : 'No child messages to peek yet'}
              onClick={() => setPeekOpen((p) => !p)}
            />
            <AffordanceButton
              testId="session-reply"
              icon={<ArrowBendUpLeft size={11} weight="regular" aria-hidden="true" />}
              label="Reply"
              disabled={!canReply || !affordancesWired}
              disabledTitle={
                !affordancesWired
                  ? 'Reply routes through chat (FE-3)'
                  : !canReply
                    ? 'No open question to reply to'
                    : undefined
              }
              onClick={
                affordancesWired && canReply
                  ? () => onSessionAction?.('reply', actionTarget)
                  : undefined
              }
            />
            <AffordanceButton
              testId="session-steer"
              icon={<Compass size={11} weight="regular" aria-hidden="true" />}
              label="Steer"
              disabled={!canSteer || !affordancesWired}
              disabledTitle={
                !affordancesWired
                  ? 'Steer routes through chat (FE-3)'
                  : !canSteer
                    ? 'Session not steerable'
                    : undefined
              }
              onClick={
                affordancesWired && canSteer
                  ? () => onSessionAction?.('steer', actionTarget)
                  : undefined
              }
            />
            <AffordanceButton
              testId="session-stop"
              icon={<StopCircle size={11} weight="regular" aria-hidden="true" />}
              label="Stop"
              tone="danger"
              disabled={!canStop || !affordancesWired}
              disabledTitle={
                !affordancesWired
                  ? 'Not wired'
                  : !canStop
                    ? 'No session id to target'
                    : undefined
              }
              onClick={
                affordancesWired && canStop
                  ? () => onSessionAction?.('stop', actionTarget)
                  : undefined
              }
            />
          </div>

          {/* FE-5 + FE-7: the live child-message list. Every row is
              untrusted-origin child text → rendered through <UntrustedChildText>
              (plain text / sanctioned markdown, no raw HTML, non-clickable
              links, untrusted chrome always visible). */}
          {peekOpen && canPeek && (
            <ul data-testid="session-message-list" className="space-y-1 mt-1">
              {(sessionMessages ?? []).slice(-6).map((m) => (
                <li
                  key={m.messageId}
                  data-testid="session-message-row"
                  data-kind={m.kind}
                  className="flex flex-col gap-0.5"
                >
                  <span className="flex items-center gap-1 text-[9px] uppercase tracking-wide text-[var(--color-muted)] font-mono">
                    {messageKindLabel(m.kind)}
                    {typeof m.pct === 'number' && (
                      <span className="text-[var(--color-accent)] normal-case">{m.pct}%</span>
                    )}
                    <span className="normal-case opacity-60">· {m.senderIdentity}</span>
                  </span>
                  {m.text && (
                    <UntrustedChildText
                      text={m.text}
                      untrustedOrigin={m.untrustedOrigin}
                      originLabel={`child agent ${m.senderIdentity}`}
                      testId="session-message-body"
                    />
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {canExpand && expanded && item.kind === 'judge' && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] pl-3 py-1 space-y-2" data-testid="judge-verdict-detail">
          {/* Per-criterion verdict list (ADR-049 D2/D4/US-13/SD-C11). `text`
              is the raw criterion_id — this global feed has no title lookup
              (see JudgeActivityItem's doc comment). */}
          <ul className="space-y-1">
            {item.criterionVerdicts.map((cv, idx) => (
              <li key={idx} className="flex items-start gap-1.5 text-[10px]">
                {cv.met ? (
                  <Check size={11} weight="bold" className="shrink-0 mt-0.5 text-[color:var(--color-success)]" aria-hidden="true" />
                ) : (
                  <X size={11} weight="bold" className="shrink-0 mt-0.5 text-[color:var(--color-error)]" aria-hidden="true" />
                )}
                <div className="min-w-0">
                  <p className="font-mono text-[var(--color-secondary)] truncate">{cv.text}</p>
                  {cv.reason && <p className="text-[var(--color-muted)] whitespace-pre-wrap mt-0.5">{cv.reason}</p>}
                </div>
              </li>
            ))}
            {item.criterionVerdicts.length === 0 && (
              <li className="text-[10px] text-[var(--color-muted)] italic">No per-criterion detail recorded.</li>
            )}
          </ul>
          {/* Model + agent + spend footer — NFR-5 transparency. Spend is
              deliberately omitted: JudgeVerdictFrame carries no tokens/cost
              field on the wire today (contract gap flagged in this wave's
              report; only the persisted Message twin carries tokens/cost). */}
          <p className="text-[10px] text-[var(--color-muted)]">
            {item.model} · judged by {item.judgeAgentId}
          </p>
        </div>
      )}

      {canExpand && expanded && item.kind !== 'judge' && steps && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] pl-3 py-1 space-y-1">
          {/* surface="panel" (Fix 2, user-approved 2026-07-16): this panel is
              the designated home for the background/noisy step detail the
              thread hides by default — its policy INVERTS to show
              everything except load_tool (shouldRenderToolCallInPanel in
              toolVisibility.ts), instead of ToolCallBadge's normal
              thread-scoped shouldRenderToolCall gate. */}
          {steps.map((step, idx) =>
            step.kind === 'tool' ? (
              <ToolCallBadge key={step.tool.call_id} toolCall={step.tool} surface="panel" />
            ) : (
              <p key={idx} className="text-[10px] text-[var(--color-secondary)] font-sans py-0.5">
                {step.text}
              </p>
            ),
          )}

          {/* Final result — flat text block, no box/fill: a small success
              dot + muted "Final result" label, matching SubagentBlock's own
              treatment (the thread card this replaces at idle/default). */}
          {finalResult && (
            <div className="mt-1">
              <div className="flex items-center gap-1.5 text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide font-sans">
                {statusDot('bg-[var(--color-success)]')}
                Final result
              </div>
              <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all font-mono">
                {finalResult}
              </pre>
            </div>
          )}
        </div>
      )}

      {show3pNotice && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] pl-3 py-1">
          <p className="text-[10px] text-[var(--color-muted)] italic">No live step detail yet</p>
        </div>
      )}
    </div>
  )
}

/** A compact affordance button for the FE-5 session-list action row. */
function AffordanceButton({
  icon,
  label,
  onClick,
  disabled,
  disabledTitle,
  active,
  tone,
  testId,
}: {
  icon: React.ReactNode
  label: string
  onClick?: () => void
  disabled?: boolean
  disabledTitle?: string
  active?: boolean
  tone?: 'danger'
  testId: string
}) {
  return (
    <button
      type="button"
      tabIndex={disabled ? -1 : 0}
      disabled={disabled}
      onClick={onClick}
      data-testid={testId}
      aria-label={label}
      aria-pressed={active}
      title={disabled ? (disabledTitle ?? label) : label}
      className={cn(
        'inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[9px] font-mono uppercase tracking-wide border transition-colors',
        disabled
          ? 'opacity-40 cursor-not-allowed border-[var(--color-border)] text-[var(--color-muted)]'
          : tone === 'danger'
            ? 'border-[var(--color-error)]/50 text-[var(--color-error)] hover:bg-[var(--color-error)]/10 cursor-pointer'
            : active
              ? 'border-[var(--color-accent)] text-[var(--color-accent)] bg-[var(--color-accent)]/10 cursor-pointer'
              : 'border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] cursor-pointer',
      )}
    >
      {icon}
      {label}
    </button>
  )
}

export function ActivityPanel({
  open,
  onOpenChange,
  running,
  recentlyFinished,
  onSessionAction,
}: ActivityPanelProps) {
  const isEmpty = running.length === 0 && recentlyFinished.length === 0

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-[90vw] sm:w-[22.5rem] p-0 flex flex-col" overlay={false}>
        <SheetHeader>
          <SheetTitle>Activity</SheetTitle>
          <Badge variant={running.length > 0 ? 'default' : 'muted'}>{running.length} running</Badge>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto px-3 py-3 space-y-4">
          {isEmpty && (
            <p className="text-xs text-[var(--color-muted)] text-center py-6">No background activity yet.</p>
          )}

          {running.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] px-1">
                Running now
              </h3>
              <div className="space-y-1">
                {running.map((item) => (
                  <ActivityRow key={item.key} item={item} onSessionAction={onSessionAction} />
                ))}
              </div>
            </div>
          )}

          {recentlyFinished.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] px-1">
                Recently finished
              </h3>
              <div className="space-y-1">
                {recentlyFinished.map((item) => (
                  <ActivityRow key={item.key} item={item} onSessionAction={onSessionAction} />
                ))}
              </div>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
