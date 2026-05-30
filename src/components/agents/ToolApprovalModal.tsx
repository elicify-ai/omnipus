// ToolApprovalModal — FR-011, FR-082, FR-052, FR-073
//
// Renders a modal for pending tool-policy approval requests. Driven by the
// useToolApprovalStore queue. Shows one approval at a time; subsequent
// approvals queue behind the visible one.
//
// Countdown uses expires_in_ms (relative on receipt) — stored as an absolute
// local timestamp (expiresAt = Date.now() + expires_in_ms) so the countdown
// is independent of gateway clock skew.
//
// Buttons:
//   Approve → POST /api/v1/tool-approvals/{id} {action:"approve"}
//   Deny    → POST /api/v1/tool-approvals/{id} {action:"deny"}
//   Cancel  → POST /api/v1/tool-approvals/{id} {action:"cancel"}
//
// Error handling:
//   401 → re-auth toast (user must log in again)
//   403 → "you must be an admin to approve this tool" toast
//   410 → "this approval has already been resolved" → dismiss modal entry

import { useEffect, useState, useCallback } from 'react'
import { CheckCircle, XCircle, ProhibitInset, Shield } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { useToolApprovalStore } from '@/store/toolApproval'
import { postToolApproval, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

function useCountdown(expiresAt: number): { remainingMs: number; progressPct: number; totalMs: number } {
  const [remainingMs, setRemainingMs] = useState(() => Math.max(0, expiresAt - Date.now()))
  // Capture the total duration once so the progress bar doesn't jump
  const [totalMs] = useState(() => Math.max(1, expiresAt - Date.now()))

  useEffect(() => {
    setRemainingMs(Math.max(0, expiresAt - Date.now()))
    const interval = setInterval(() => {
      const left = Math.max(0, expiresAt - Date.now())
      setRemainingMs(left)
      if (left === 0) clearInterval(interval)
    }, 500)
    return () => clearInterval(interval)
  }, [expiresAt])

  return {
    remainingMs,
    progressPct: totalMs > 0 ? ((totalMs - remainingMs) / totalMs) * 100 : 100,
    totalMs,
  }
}

function formatCountdown(ms: number): string {
  if (ms <= 0) return 'Expired'
  const secs = Math.ceil(ms / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  const remainSecs = secs % 60
  return `${mins}m ${remainSecs}s`
}

interface ToolApprovalCardProps {
  approvalId: string
  toolName: string
  args: Record<string, unknown>
  agentId: string
  expiresAt: number
  queueLength: number
}

function ToolApprovalCard({
  approvalId,
  toolName,
  args,
  agentId,
  expiresAt,
  queueLength,
}: ToolApprovalCardProps) {
  const dequeue = useToolApprovalStore((s) => s.dequeue)
  const addToast = useUiStore((s) => s.addToast)
  const [submitting, setSubmitting] = useState(false)
  const { remainingMs, progressPct } = useCountdown(expiresAt)

  const hasExpired = remainingMs <= 0

  const handleAction = useCallback(
    async (action: 'approve' | 'deny' | 'cancel') => {
      if (submitting) return
      setSubmitting(true)
      try {
        await postToolApproval(approvalId, action)
        dequeue(approvalId)
      } catch (err) {
        if (isApiError(err)) {
          if (err.status === 401) {
            addToast({
              message: 'Session expired — please log in again to approve tool calls.',
              variant: 'error',
            })
          } else if (err.status === 403) {
            addToast({
              message: 'You must be an admin to approve this tool.',
              variant: 'error',
            })
          } else if (err.status === 410) {
            // Already resolved — silently dismiss
            dequeue(approvalId)
          } else {
            addToast({
              message: `Failed to submit approval: ${err.userMessage}`,
              variant: 'error',
            })
          }
        } else {
          const message = err instanceof Error ? err.message : String(err)
          addToast({
            message: `Failed to submit approval: ${message}`,
            variant: 'error',
          })
        }
      } finally {
        setSubmitting(false)
      }
    },
    [approvalId, dequeue, addToast, submitting],
  )

  const argsJson = JSON.stringify(args, null, 2)

  return (
    <div
      className={cn(
        'fixed inset-0 z-50 flex items-center justify-center p-4',
        'bg-black/60 backdrop-blur-sm',
      )}
    >
      <div
        className={cn(
          'w-full max-w-lg rounded-xl border shadow-2xl',
          'bg-[var(--color-surface-1)] border-[var(--color-warning)]/40',
          'flex flex-col gap-0 overflow-hidden',
        )}
        role="dialog"
        aria-modal="true"
        aria-label={`Approve tool call: ${toolName}`}
      >
        {/* Header */}
        <div className="flex items-center gap-3 px-5 py-4 border-b border-[var(--color-border)]">
          <Shield
            size={20}
            weight="bold"
            className="text-[var(--color-warning)] shrink-0"
          />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-semibold text-[var(--color-secondary)]">
              Tool Approval Required
            </p>
            <p className="text-xs text-[var(--color-muted)] truncate">
              Agent: <span className="font-mono">{agentId}</span>
            </p>
          </div>
          {queueLength > 1 && (
            <span className="shrink-0 text-[10px] bg-[var(--color-surface-2)] text-[var(--color-muted)] px-2 py-0.5 rounded-full">
              +{queueLength - 1} more
            </span>
          )}
        </div>

        {/* Tool info */}
        <div className="px-5 py-4 space-y-3">
          <div>
            <p className="text-xs text-[var(--color-muted)] mb-1">Tool</p>
            <p className="font-mono text-sm text-[var(--color-accent)] font-semibold">
              {toolName}
            </p>
          </div>

          {args && Object.keys(args).length > 0 && (
            <div>
              <p className="text-xs text-[var(--color-muted)] mb-1">Arguments</p>
              <pre className="text-xs font-mono bg-[var(--color-surface-2)] rounded-lg px-3 py-2 overflow-auto max-h-40 whitespace-pre-wrap break-all text-[var(--color-secondary)]">
                {argsJson}
              </pre>
            </div>
          )}
        </div>

        {/* Countdown */}
        <div className="px-5 pb-3">
          {hasExpired ? (
            <p className="text-xs text-[var(--color-error)] flex items-center gap-1">
              <XCircle size={13} weight="fill" />
              Approval expired — the agent will receive a denial.
            </p>
          ) : (
            <>
              <div className="flex items-center justify-between mb-1.5">
                <p className="text-xs text-[var(--color-muted)]">Expires in</p>
                <p className="text-xs font-mono text-[var(--color-secondary)]">
                  {formatCountdown(remainingMs)}
                </p>
              </div>
              <Progress
                value={progressPct}
                className="h-1"
              />
            </>
          )}
        </div>

        {/* Action buttons */}
        {!hasExpired && (
          <div className="flex gap-2 px-5 py-4 border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
            <Button
              size="sm"
              variant="default"
              onClick={() => handleAction('approve')}
              disabled={submitting}
              className="h-8 text-xs flex-1 sm:flex-none"
            >
              <CheckCircle size={14} weight="bold" />
              Approve
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => handleAction('deny')}
              disabled={submitting}
              className="h-8 text-xs flex-1 sm:flex-none"
            >
              <XCircle size={14} weight="bold" />
              Deny
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => handleAction('cancel')}
              disabled={submitting}
              className="h-8 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] ml-auto"
            >
              <ProhibitInset size={14} />
              Cancel
            </Button>
          </div>
        )}

        {hasExpired && (
          <div className="px-5 py-4 border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => dequeue(approvalId)}
              className="h-8 text-xs w-full text-[var(--color-muted)]"
            >
              Dismiss
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

// ToolApprovalModal renders the front-of-queue approval, if any.
export function ToolApprovalModal() {
  const queue = useToolApprovalStore((s) => s.queue)
  const first = queue[0]

  if (!first) return null

  return (
    <ToolApprovalCard
      key={first.approvalId}
      approvalId={first.approvalId}
      toolName={first.toolName}
      args={first.args}
      agentId={first.agentId}
      expiresAt={first.expiresAt}
      queueLength={queue.length}
    />
  )
}
