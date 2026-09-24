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
// ADR-091 D7/FR-E-004/FR-E-009: each agent row now carries its own status
// line (the last `subagent_message.text` reduced onto the span, or "last
// update N s ago" before one arrives — see SubagentSpanBase.statusLine's
// doc comment) and an open control that navigates to the child's own
// session (`/sessions/$sessionId`, the same standalone attach route a task
// session opens through today). A `queued` lifecycle state overrides the
// dot's label; a pending tool approval for the child's session id
// (`src/store/toolApproval.ts`) overrides the status LINE with "awaiting
// approval: <tool>" until it resolves. The nested per-step detail
// (SubagentBlock's steps, ToolCallBadge surface="panel") is gone — a
// child's own tool calls carry the child's own session_id (I-4) and never
// arrive in this bucket any more; a delegation's steps are visible only in
// the child's own session, reached via the open control. Final result and
// interrupt reason (subagent_end's own fields, untouched by that deletion)
// stay expandable.
//
// ── ADR-057: the ADR-053 FE-5 "live session list" enrichment (lifecycle
// badge + peek/reply/steer/stop affordances riding a pair of mid-span child
// progress/lifecycle WS frame types) has been REMOVED. Those frame types
// have zero Go emitters and are absent from the `WsFrameType` enum in
// contracts, Go and TS (ADR-057 Explicit Non-Behaviors — see that section
// for the frame type names) — the dedicated store that fed this block was
// therefore permanently empty in production, making the entire block
// (badge, peek, and all three reply/steer/stop buttons) unreachable dead
// code. It is not being replaced; a real implementation would need a real
// wire mechanism first. ADR-091 D7 replaces it for real: the status line and
// open control above are the wired-up version of the same idea.

import { useState } from 'react'
import { Check, X, ArrowSquareOut } from '@phosphor-icons/react'
import { useNavigate } from '@tanstack/react-router'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { ActivityAvatar } from './ActivityAvatar'
import type { ActivityItem } from '@/hooks/useRunningActivity'
import { useToolApprovalStore } from '@/store/toolApproval'
import { cn } from '@/lib/utils'
import { formatDuration } from '@/lib/formatDuration'
import { getSpanStatusDot, statusDot } from '@/lib/toolStatusConfig'
import { formatInterruptReason, formatLastUpdateAge, getLifecycleStatusDot } from '@/lib/subagentStatus'

export interface ActivityPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  running: ActivityItem[]
  recentlyFinished: ActivityItem[]
}

function ActivityRow({
  item,
}: {
  item: ActivityItem
}) {
  const navigate = useNavigate()
  const [expanded, setExpanded] = useState(false)
  // JudgeActivityItem carries no durationMs (no wire-level "judge started"
  // moment to measure elapsed time from — see its doc comment).
  const duration = item.kind === 'judge' ? '' : formatDuration(item.durationMs)
  const label = item.kind === 'bash' ? item.command : item.kind === 'judge' ? `Judge · ${item.scope} round ${item.round}` : item.taskLabel
  const show3pNotice = item.kind === 'agent' && item.agentType === '3p'
  // Fix 2 (2026-07-16): the panel is now the durable surface for the final
  // result / interrupt reason SubagentBlock's (now-deleted) card used to
  // carry — see useRunningActivity.ts's AgentActivityItem.
  const finalResult = item.kind === 'agent' ? item.finalResult : undefined
  const interruptReason = item.kind === 'agent' ? item.interruptReason : undefined
  const childSessionId = item.kind === 'agent' ? item.childSessionId : undefined
  const lifecycleState = item.kind === 'agent' ? item.lifecycleState : undefined
  // ADR-091 D7/FR-E-004 (cross-family review finding 21): lifecycleState
  // (the ADR-053 eight-state domain reduced from subagent_state) drives the
  // row's dot AND label whenever it is present — a span's own `status` (the
  // parent's "still open" flag, set at subagent_start/cleared at
  // subagent_end) is a different axis and is only a legacy fallback for a
  // span with no lifecycleState yet. Previously only the LABEL was
  // overridden, and only for 'queued' — needs_input/paused/completed/
  // failed/cancelled/timed_out all kept showing the running spinner.
  const config = lifecycleState
    ? getLifecycleStatusDot(lifecycleState, { size: 12, runningLabel: 'running' })
    : getSpanStatusDot(item.status, { size: 12, runningLabel: 'running' })

  // FR-E-009: a pending approval for the child's session overrides the
  // status LINE (not the dot label above) with "awaiting approval: <tool>"
  // until it resolves — read live from the browser's approval queue, no
  // new frame.
  const approvalQueue = useToolApprovalStore((s) => s.queue)
  const pendingApproval = childSessionId
    ? approvalQueue.find((a) => a.sessionId === childSessionId)
    : undefined

  const rawStatusLine = item.kind === 'agent' ? item.statusLine : undefined
  const lastUpdateAt = item.kind === 'agent' ? item.lastUpdateAt : undefined
  const fallbackStatusLine = lastUpdateAt
    ? `last update ${formatLastUpdateAge(Date.now() - Date.parse(lastUpdateAt))}`
    : undefined
  const statusLine = pendingApproval
    ? `awaiting approval: ${pendingApproval.toolName}`
    : (rawStatusLine ?? fallbackStatusLine)

  // ADR-049 D2/D4/US-13: a judge row is ALWAYS expandable — it has no step
  // detail at all (there is no live "judge started" frame, only the
  // completed verdict push), but the per-criterion list is exactly the
  // "zero step detail but a final verdict" case the BDD edge case calls out
  // ("Judge span with zero step detail but a verdict — must stay expandable").
  const canExpandJudge = item.kind === 'judge'

  // ADR-091 D10: a span carries no step detail any more (D7 table) — the
  // only expandable content left for an agent row is its final result.
  const canExpand = canExpandJudge || !!finalResult

  return (
    <div
      data-testid="activity-row"
      data-status={item.status}
      className="text-[length:var(--type-utility-xs-size)]"
    >
      {/* Mirrors GenericToolCall.tsx's `disabled={!hasDetail}` gate: a
          non-expandable row (bash calls, 3p-agent rows, or an agent row with
          no final result) has nothing to expand — disable it natively so it
          drops out of the tab order and Enter/Space can't no-op on it,
          rather than leaving a focusable dead button whose aria-expanded is
          already (correctly) omitted below. */}
      <DisclosureRow
        expanded={expanded}
        onExpandedChange={setExpanded}
        expandable={canExpand}
        caretSize={11}
        data-testid="activity-row-toggle"
      >
        <ActivityAvatar item={item} size="sm" />
        <span className="flex-1 min-w-0 truncate text-[var(--color-secondary)] font-medium font-mono">
          {label}
        </span>
        {config.indicator}
        <span className={cn('text-[var(--color-muted)] shrink-0')}>
          {config.label}
          {/* W1-9, carried via Fix 2: interrupt reason appended to the
              status text, matching SubagentBlock's own inline treatment. */}
          {item.status === 'interrupted' && interruptReason && (
            <span
              className="font-body"
              title={`Interrupted: ${formatInterruptReason(interruptReason)}`}
            >
              {' '}({formatInterruptReason(interruptReason)})
            </span>
          )}
        </span>
        {duration && <span className="text-[var(--color-muted)] shrink-0 tabular-nums">{duration}</span>}
      </DisclosureRow>

      {/* ADR-091 D7/FR-E-004: the row's one status line. `queued` renders
          without one (the dot above already reads "queued" — no fabricated
          line for a child that has not yet started). Absent (an
          un-upgraded transcript, or before any message/state has arrived)
          renders nothing. Indented to align under the row's label. */}
      {statusLine && lifecycleState !== 'queued' && (
        <p
          data-testid="activity-row-status-line"
          className="pl-[var(--space-5)] pb-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] font-body truncate"
        >
          {statusLine}
        </p>
      )}

      {/* ADR-091 D7/FR-E-004: the open control — targets the child's own
          session, when the transcript carries one (a pre-delivery
          `subagent_start` without `child_session_id` renders without it).
          Reuses the existing standalone session route, the same one a task
          session opens through today. Sibling of the DisclosureRow, never
          nested inside it: the row header is itself a button. */}
      {childSessionId && (
        <Button
          type="button"
          variant="link"
          size="sm"
          data-testid="activity-row-open"
          onClick={() => void navigate({ to: '/sessions/$sessionId', params: { sessionId: childSessionId } })}
          className="ml-[var(--space-5)] mb-[var(--space-1)] h-auto justify-start gap-[var(--space-1)] px-0 py-0 text-[length:var(--type-caption-size)]"
        >
          <ArrowSquareOut size={11} aria-hidden="true" />
          Open
        </Button>
      )}

      {canExpand && expanded && item.kind === 'judge' && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] pl-[var(--space-2-5)] py-[var(--space-1)] space-y-[var(--space-2)]" data-testid="judge-verdict-detail">
          {/* Per-criterion verdict list (ADR-049 D2/D4/US-13/SD-C11). `text`
              is the raw criterion_id — this global feed has no title lookup
              (see JudgeActivityItem's doc comment). */}
          <ul className="space-y-[var(--space-1)]">
            {item.criterionVerdicts.map((cv, idx) => (
              <li key={idx} className="flex items-start gap-[var(--space-1)] text-[length:var(--type-caption-size)]">
                {cv.met ? (
                  <Check size={11} weight="bold" className="shrink-0 mt-[var(--space-0-5)] text-[color:var(--color-success)]" aria-hidden="true" />
                ) : (
                  <X size={11} weight="bold" className="shrink-0 mt-[var(--space-0-5)] text-[color:var(--color-error)]" aria-hidden="true" />
                )}
                <div className="min-w-0">
                  <p className="font-mono text-[var(--color-secondary)] truncate">{cv.text}</p>
                  {cv.reason && <p className="text-[var(--color-muted)] whitespace-pre-wrap mt-[var(--space-0-5)]">{cv.reason}</p>}
                </div>
              </li>
            ))}
            {item.criterionVerdicts.length === 0 && (
              <li className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic">No per-criterion detail recorded.</li>
            )}
          </ul>
          {/* Model + agent + spend footer — NFR-5 transparency. Spend is
              deliberately omitted: JudgeVerdictFrame carries no tokens/cost
              field on the wire today (contract gap flagged in this wave's
              report; only the persisted Message twin carries tokens/cost). */}
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            {item.model} · judged by {item.judgeAgentId}
          </p>
        </div>
      )}

      {canExpand && expanded && item.kind !== 'judge' && finalResult && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] pl-[var(--space-2-5)] py-[var(--space-1)] space-y-[var(--space-1)]">
          {/* ADR-091 D10: the per-step detail this block used to carry
              (SubagentBlock's steps via ToolCallBadge surface="panel") is
              deleted — a child's own tool calls carry the child's own
              session_id (I-4) and never arrive in this bucket. Final result
              (subagent_end's own field, untouched by that deletion) is the
              only expandable content left. */}
          <div className="mt-[var(--space-1)]">
            <div className="flex items-center gap-[var(--space-1)] text-[var(--color-muted)] mb-[var(--space-1)] text-[length:var(--type-caption-size)] uppercase tracking-wide font-body">
              {statusDot('bg-[var(--color-success)]')}
              Final result
            </div>
            <pre className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all font-mono">
              {finalResult}
            </pre>
          </div>
        </div>
      )}

      {show3pNotice && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] pl-[var(--space-2-5)] py-[var(--space-1)]">
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic">No live step detail yet</p>
        </div>
      )}
    </div>
  )
}

export function ActivityPanel({
  open,
  onOpenChange,
  running,
  recentlyFinished,
}: ActivityPanelProps) {
  const isEmpty = running.length === 0 && recentlyFinished.length === 0

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-[90vw] sm:w-[22.5rem] p-0 flex flex-col" overlay={false}>
        <SheetHeader>
          <SheetTitle>Activity</SheetTitle>
          <Badge variant={running.length > 0 ? 'default' : 'muted'}>{running.length} running</Badge>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto px-[var(--space-2-5)] py-[var(--space-2-5)] space-y-[var(--space-3)]">
          {isEmpty && (
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] text-center py-[var(--space-4)]">No background activity yet.</p>
          )}

          {running.length > 0 && (
            <div className="space-y-[var(--space-2)]">
              <h3 className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)] px-[var(--space-1)]">
                Running now
              </h3>
              <div className="space-y-[var(--space-1)]">
                {running.map((item) => (
                  <ActivityRow key={item.key} item={item} />
                ))}
              </div>
            </div>
          )}

          {recentlyFinished.length > 0 && (
            <div className="space-y-[var(--space-2)]">
              <h3 className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)] px-[var(--space-1)]">
                Recently finished
              </h3>
              <div className="space-y-[var(--space-1)]">
                {recentlyFinished.map((item) => (
                  <ActivityRow key={item.key} item={item} />
                ))}
              </div>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
