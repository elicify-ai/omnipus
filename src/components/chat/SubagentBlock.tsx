// SubagentBlock — FR-H-008
// Renders one subagent span as a collapsible flat text-line row (ticket "Tool
// components in chat", P2 — delegate-card restyle). Matches ToolCallBadge's/
// GenericToolCall's flat grammar: no bordered/filled card, no status pill —
// an 8px status dot (or the spinning icon while running) + a "Delegate → "
// label + muted status/duration text + a muted caret. Expanded body swaps
// the old bordered panel for an indented border-l-2 accent line, same as the
// two tool-call components; the final-result section drops its bordered box
// in favor of a plain text block with a small success dot + muted label.
//
// Status dot comes from getSpanStatusDot (src/lib/toolStatusConfig.tsx),
// added in db5714b6 for this migration. ActivityPanel.tsx uses the same
// helper. The pill-shaped predecessor (getSpanStatusConfig) had no
// remaining consumers once both callers migrated, so it was deleted rather
// than kept as dead code.

import { CaretDown, CaretUp } from '@phosphor-icons/react'
import { ToolCallBadge } from './ToolCallBadge'
import type { SubagentSpan, SubagentSpanTerminal } from '@/store/chat'
import type { WsSubagentEndFrame } from '@/lib/ws'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import { formatDuration } from '@/lib/formatDuration'
import { getSpanStatusDot, statusDot } from '@/lib/toolStatusConfig'

type SubagentEndReason = WsSubagentEndFrame['reason']

// ── Label truncation — grapheme-safe (FR-H-009, Scenario 14) ─────────────────

/** Truncate to 60 grapheme clusters. Uses Array.from for emoji/CJK safety. */
function truncateLabel(raw: string): string {
  const clusters = Array.from(raw)
  if (clusters.length <= 60) return raw
  return clusters.slice(0, 60).join('') + '…'
}

// ── Duration formatting + status config ──────────────────────────────────────
// Shared with ActivityPanel.tsx (span family) — see src/lib/toolStatusConfig.tsx.
// SubagentBlock uses the defaults: icon size 13, 'running' label "working".

// ── Step count text ───────────────────────────────────────────────────────────

function stepCountText(count: number): string {
  if (count === 1) return '1 step'
  return `${count} steps`
}

// ── SubagentBlock ─────────────────────────────────────────────────────────────

/** Human-readable label for the interrupted reason field (W1-9). */
function formatInterruptReason(reason: SubagentEndReason): string {
  switch (reason) {
    case 'parent_timeout': return 'parent timed out'
    case 'parent_cancelled': return 'parent cancelled'
    case 'parent_done_early': return 'parent completed early'
    case 'unknown': return 'unknown reason'
    default: return reason ?? ''
  }
}

export interface SubagentBlockProps {
  span: SubagentSpan
}

export function SubagentBlock({ span }: SubagentBlockProps) {
  // Expansion state lives in the UI store so live-render → historical-virtualized-render
  // swap (when streaming ends) preserves user-chosen expanded/collapsed state.
  const expanded = useUiStore((s) => Boolean(s.expandedSpans[span.spanId]))
  const toggleSpanExpansion = useUiStore((s) => s.toggleSpanExpansion)
  const isTerminal = span.status !== 'running'

  // W4-4: narrow to terminal type before accessing durationMs/finalResult.
  const terminal = isTerminal ? (span as SubagentSpanTerminal) : null
  const config = getSpanStatusDot(span.status)
  const label = truncateLabel(span.taskLabel ?? '')
  const stepCount = span.steps.length
  const hasFinalResult = Boolean(terminal?.finalResult)

  function toggle() {
    toggleSpanExpansion(span.spanId)
  }

  return (
    // Flat text-line design: no border, no surface fill, no rounded frame —
    // the row is transparent on the thread, matching ToolCallBadge/GenericToolCall.
    <div className="mt-2 text-xs font-mono">
      {/* Collapsed header — FR-H-008 */}
      <button tabIndex={0}
        type="button"
        data-testid="subagent-collapsed"
        onClick={toggle}
        aria-expanded={expanded}
        aria-label={`Subagent: ${label}, ${stepCountText(stepCount)}, status ${span.status}`}
        className="flex w-full items-center gap-2 py-1 text-left transition-colors hover:bg-[var(--color-surface-2)]/60 cursor-pointer"
      >
        {/* Status indicator — dot for terminal states, spinner while running */}
        {config.indicator}

        {/* Delegate label — the exact same truncated task label as before,
            prefixed to read as a delegation row now that the dedicated
            subagent icon + bordered card are gone. */}
        <span className="text-[var(--color-secondary)] font-medium truncate flex-1 min-w-0">
          Delegate → {label}
        </span>

        {/* Step count */}
        <span data-testid="subagent-step-counter" className="text-[var(--color-muted)] shrink-0 tabular-nums">
          {stepCountText(stepCount)}
        </span>

        {/* Status text — muted, per the flat text-line spec */}
        <span className={cn('text-[var(--color-muted)] shrink-0', config.textClass)}>
          {config.label}
          {/* W1-9: show interrupt reason as a muted inline label when available */}
          {span.status === 'interrupted' && terminal?.reason && (
            <span
              className="font-sans"
              title={`Interrupted: ${formatInterruptReason(terminal.reason)}`}
            >
              {' '}({formatInterruptReason(terminal.reason)})
            </span>
          )}
        </span>

        {/* Duration — only shown in terminal state */}
        {terminal?.durationMs != null && (
          <span className="text-[var(--color-muted)] shrink-0 tabular-nums">
            {formatDuration(terminal.durationMs)}
          </span>
        )}

        {/* Caret */}
        <span className="ml-auto text-[var(--color-muted)] shrink-0">
          {expanded ? <CaretUp size={12} aria-hidden="true" /> : <CaretDown size={12} aria-hidden="true" />}
        </span>
      </button>

      {/* Expanded body — FR-H-008. Indented quote-block: a thin left accent
          line stands in for the old bordered panel, matching ToolCallBadge's/
          GenericToolCall's expanded-detail treatment. */}
      {expanded && (
        <div
          data-testid="subagent-expanded"
          className="ml-[3px] border-l-2 border-[var(--color-border)] py-1 pl-3 space-y-1"
          style={{ maxHeight: '400px', overflowY: 'auto' }}
        >
          {span.steps.length === 0 && !hasFinalResult && (
            <p className="text-[var(--color-muted)] text-[11px] py-1">No steps recorded.</p>
          )}

          {/* Steps — in arrival order. W4-5: switch on step.kind */}
          {span.steps.map((step, idx) => {
            if (step.kind === 'tool') {
              return (
                <div key={step.tool.call_id} data-testid="subagent-live-step">
                  <ToolCallBadge toolCall={step.tool} />
                </div>
              )
            }
            // kind === 'text' — reserved for future subagent-text streaming
            return (
              <p key={idx} data-testid="subagent-live-step" className="text-[10px] text-[var(--color-secondary)] font-sans py-0.5">
                {step.text}
              </p>
            )
          })}

          {/* Final result section — plain text block, no box/fill: a small
              success dot + muted "Final result" label, matching the flat
              text-line design used everywhere else. */}
          {hasFinalResult && (
            <div className="mt-2">
              <div className="flex items-center gap-1.5 text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide font-sans">
                {statusDot('bg-[var(--color-success)]')}
                Final result
              </div>
              <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all font-mono">
                {terminal?.finalResult}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
