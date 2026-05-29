// SubagentBlock — FR-H-008
// Renders one subagent span as a collapsible block.
// Collapsed header: icon + task label + step count + status pill + duration + caret.
// Expanded body: nested ToolCallBadges + optional final result section.
// Visual grammar matches ToolCallBadge (same border/surface palette).

import {
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  Prohibit,
  Clock,
  CaretDown,
  CaretUp,
  UserCircle,
} from '@phosphor-icons/react'
import { ToolCallBadge } from './ToolCallBadge'
import type { SubagentSpan, SubagentSpanTerminal } from '@/store/chat'
import type { WsSubagentEndFrame } from '@/lib/ws'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

type SubagentEndReason = WsSubagentEndFrame['reason']

// ── Label truncation — grapheme-safe (FR-H-009, Scenario 14) ─────────────────

/** Truncate to 60 grapheme clusters. Uses Array.from for emoji/CJK safety. */
function truncateLabel(raw: string): string {
  const clusters = Array.from(raw)
  if (clusters.length <= 60) return raw
  return clusters.slice(0, 60).join('') + '\u2026'
}

// ── Duration formatting ───────────────────────────────────────────────────────

function formatDuration(ms?: number): string {
  if (ms == null) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

// ── Status config ─────────────────────────────────────────────────────────────

type SpanStatus = SubagentSpan['status']

interface StatusConfig {
  icon: React.ReactNode
  label: string
  border: string
  pill: string
}

function getStatusConfig(status: SpanStatus, durationMs?: number): StatusConfig {
  switch (status) {
    case 'running':
      return {
        icon: <ArrowsClockwise size={13} className="animate-spin text-[var(--color-accent)]" aria-hidden="true" />,
        label: 'working',
        border: 'border-[var(--color-border)]',
        pill: 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
      }
    case 'success':
      return {
        icon: <CheckCircle size={13} className="text-[var(--color-success)]" weight="fill" aria-hidden="true" />,
        label: durationMs ? formatDuration(durationMs) : 'done',
        border: 'border-[var(--color-success)]/20',
        pill: 'bg-[var(--color-success)]/10 text-[var(--color-success)]',
      }
    case 'error':
      return {
        icon: <XCircle size={13} className="text-[var(--color-error)]" weight="fill" aria-hidden="true" />,
        label: durationMs ? formatDuration(durationMs) : 'failed',
        border: 'border-[var(--color-error)]/20',
        pill: 'bg-[var(--color-error)]/10 text-[var(--color-error)]',
      }
    case 'cancelled':
      return {
        icon: <Prohibit size={13} className="text-[var(--color-cancelled)]" weight="fill" aria-hidden="true" />,
        label: 'cancelled',
        border: 'border-[var(--color-cancelled)]/20',
        pill: 'bg-[var(--color-cancelled)]/10 text-[var(--color-cancelled)]',
      }
    case 'interrupted':
      return {
        icon: <Prohibit size={13} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'interrupted',
        border: 'border-[var(--color-muted)]/20',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    case 'timeout':
      // W4-2: timeout is treated like interrupted but with a Clock icon
      return {
        icon: <Clock size={13} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'timed out',
        border: 'border-[var(--color-muted)]/20',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    default: {
      // W4-6: safe fallback for any unexpected status value arriving from the wire.
      // Prevents the "unknown status → undefined → render crash" latent bug.
      const _exhaustive: never = status
      void _exhaustive
      return {
        icon: <Prohibit size={13} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'unknown',
        border: 'border-[var(--color-muted)]/20',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    }
  }
}

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
  const config = getStatusConfig(span.status, terminal?.durationMs)
  const label = truncateLabel(span.taskLabel ?? '')
  const stepCount = span.steps.length
  const hasFinalResult = Boolean(terminal?.finalResult)

  function toggle() {
    toggleSpanExpansion(span.spanId)
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLButtonElement>) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      toggle()
    }
  }

  return (
    <div
      className={cn(
        'mt-2 rounded-md border bg-[var(--color-surface-1)] text-xs font-mono overflow-hidden',
        config.border,
      )}
    >
      {/* Collapsed header — FR-H-008 */}
      <button
        type="button"
        data-testid="subagent-collapsed"
        onClick={toggle}
        onKeyDown={handleKeyDown}
        aria-expanded={expanded}
        aria-label={`Subagent: ${label}, ${stepCountText(stepCount)}, status ${span.status}`}
        className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-[var(--color-surface-2)] cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50"
      >
        {/* Subagent icon */}
        <UserCircle size={13} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />

        {/* Task label */}
        <span className="text-[var(--color-secondary)] font-medium truncate flex-1 min-w-0">
          {label}
        </span>

        {/* Step count */}
        <span data-testid="subagent-step-counter" className="text-[var(--color-muted)] shrink-0 tabular-nums">
          {stepCountText(stepCount)}
        </span>

        {/* Status indicator */}
        <span className={cn('flex items-center gap-1 shrink-0 rounded px-1.5 py-0.5', config.pill)}>
          {config.icon}
          <span>{config.label}</span>
          {/* W1-9: show interrupt reason as a muted inline label when available */}
          {span.status === 'interrupted' && terminal?.reason && (
            <span
              className="text-[var(--color-muted)] font-sans"
              title={`Interrupted: ${formatInterruptReason(terminal.reason)}`}
            >
              ({formatInterruptReason(terminal.reason)})
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

      {/* Expanded body — FR-H-008 */}
      {expanded && (
        <div
          data-testid="subagent-expanded"
          className="border-t border-[var(--color-border)] px-3 py-2 space-y-1"
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

          {/* Final result section — visually distinguishable from tool calls */}
          {hasFinalResult && (
            <div className="mt-2 rounded border border-[var(--color-success)]/30 bg-[var(--color-surface-2)] px-3 py-2">
              <div className="text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide font-sans">
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
