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
//   Approve      → POST /api/v1/tool-approvals/{id} {action:"approve"}
//   Always Allow → POST /api/v1/tool-approvals/{id} {action:"always"}
//   Deny         → POST /api/v1/tool-approvals/{id} {action:"deny"}
//   Cancel       → POST /api/v1/tool-approvals/{id} {action:"cancel"}
//
// Accessibility (C2 — this is a SECURITY-CRITICAL control):
//   - Built on the shadcn/Radix Dialog primitive, which provides a focus trap,
//     focus restoration on close, role="dialog", aria-modal="true", and
//     labelled/described associations (DialogTitle / DialogDescription).
//   - Default keyboard focus lands on the DENY button — the SAFE default. An
//     inadvertent Enter keypress on open therefore DENIES the tool call; it
//     never auto-approves.
//   - Escape and overlay/outside interaction map to the safe default: they
//     submit a DENY (when the approval can still be acted on), never an
//     approval and never a silent dismissal that would leave the agent
//     hanging. The only exception is the expired state, where the decision is
//     already made server-side and Escape merely dismisses the notice.
//   - Every button carries visible text, so each has an accessible name.
//
// Error handling:
//   401 → re-auth toast (user must log in again)
//   403 → "you must be an admin to approve this tool" toast
//   410 → "this approval has already been resolved" → dismiss modal entry
//
// ADR-036 §3.4 note: this is now the ONLY tool-approval UI — the dedicated
// exec-only flow (ExecApprovalBlock/ExecApprovalTool, WS
// exec_approval_request/response/expired frames) was retired in favor of
// this generic modal for every tool, `bash` included. Before deleting that
// flow, its rendering was compared against this one:
//   - PORTED: the readable "binary highlighted, env-prefix separated"
//     command preview + working-dir line ExecApprovalBlock showed for shell
//     commands. See formatBashCommand() below and its use in ToolApprovalCard
//     — rendered whenever toolName is "bash" and args.command is a string,
//     in addition to (not instead of) the generic Arguments JSON dump.
//   - PORTED: ExecApprovalBlock's 3-way decision (Allow / Deny / "Always
//     Allow") is now fully available here too — the Always Allow button
//     posts {action:"always"}, which the gateway resolves by approving the
//     call AND recording a session-scoped grant via ApprovalGrantStore.Record
//     (pkg/gateway/rest_tool_registry.go, commit 35447760). The wire contract
//     (ToolApprovalActionRequest.action) carries "always" for every tool, not
//     just bash — closing the gap that used to make grant-inheritance
//     (agent-delegation-spec.md FR-D8) reachable only via the retired
//     exec-only flow.

import { useEffect, useState, useCallback, useRef } from 'react'
import { CheckCircle, XCircle, ProhibitInset, Shield, Lock } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { useToolApprovalStore } from '@/store/toolApproval'
import { submitToolApproval, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

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

// Formats a `bash` command string for display: separates any leading
// `KEY=value` env-var assignments from the binary name and highlights the
// binary. Ported verbatim (in spirit) from the retired ExecApprovalBlock so
// approving a `bash` call still shows a readable command preview, not just
// the raw Arguments JSON — see the ADR-036 note atop this file.
function formatBashCommand(command: string): { envPrefix: string; binary: string; args: string } {
  const parts = command.split(' ')
  let binaryIndex = 0
  for (let i = 0; i < parts.length; i++) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*=/.test(parts[i])) {
      binaryIndex = i
      break
    }
  }
  const envPrefix = parts.slice(0, binaryIndex).join(' ')
  const afterEnv = envPrefix ? command.slice(envPrefix.length + 1) : command
  const firstSpace = afterEnv.indexOf(' ')
  const binary = firstSpace === -1 ? afterEnv : afterEnv.slice(0, firstSpace)
  const args = firstSpace === -1 ? '' : afterEnv.slice(firstSpace)
  return { envPrefix, binary, args }
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
  // Default-focus target: the Deny button (the safe default). Focusing Deny
  // means a stray Enter on open denies rather than approves.
  const denyButtonRef = useRef<HTMLButtonElement>(null)

  const hasExpired = remainingMs <= 0

  const handleAction = useCallback(
    // Action union sourced from submitToolApproval's own signature (which in
    // turn is the generated ToolApprovalActionRequest['action']) rather than
    // a hand-rolled literal, per Constraint #8.
    async (action: Parameters<typeof submitToolApproval>[1]) => {
      if (submitting) return
      setSubmitting(true)
      try {
        await submitToolApproval(approvalId, action)
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

  // Safe-default handler for Escape / overlay-click / X close. The Dialog
  // primitive requests a close; we translate that into the SAFE decision:
  //   - not expired  → submit a DENY (never an approve, never a no-op dismiss
  //     that would leave the agent hanging on a pending approval).
  //   - expired      → the decision is already made server-side; just dismiss
  //     the notice from the local queue.
  const handleDismissRequest = useCallback(() => {
    if (submitting) return
    if (hasExpired) {
      dequeue(approvalId)
    } else {
      void handleAction('deny')
    }
  }, [submitting, hasExpired, dequeue, approvalId, handleAction])

  const argsJson = JSON.stringify(args, null, 2)
  const titleId = `tool-approval-title-${approvalId}`
  const descId = `tool-approval-desc-${approvalId}`

  // Ported from the retired ExecApprovalBlock (see the ADR-036 note atop this
  // file) — a readable command preview for `bash` calls, additive to (not a
  // replacement for) the generic Arguments JSON below.
  const bashCommand =
    toolName === 'bash' && typeof args.command === 'string' && args.command.length > 0
      ? args.command
      : null
  const bashPreview = bashCommand ? formatBashCommand(bashCommand) : null
  const bashCwd = typeof args.cwd === 'string' && args.cwd.length > 0 ? args.cwd : undefined

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        // The dialog only ever transitions open→closed here (Escape, overlay
        // click, or the X button). Route every such close through the safe
        // default rather than letting Radix dismiss silently.
        if (!next) handleDismissRequest()
      }}
    >
      <DialogContent
        className="max-w-lg border-[var(--color-warning)]/40 p-0 overflow-hidden gap-0"
        aria-labelledby={titleId}
        aria-describedby={descId}
        // Land focus on Deny (safe default) instead of Radix's first-focusable
        // heuristic (which would be the X close button).
        onOpenAutoFocus={(e) => {
          if (denyButtonRef.current) {
            e.preventDefault()
            denyButtonRef.current.focus()
          }
        }}
        // Escape and outside-pointer both resolve through onOpenChange above;
        // no special-casing needed here beyond letting the default close fire.
      >
        {/* Header */}
        <DialogHeader className="flex flex-row items-center gap-3 space-y-0 px-5 py-4 border-b border-[var(--color-border)] text-left">
          <Shield
            size={20}
            weight="bold"
            className="text-[var(--color-warning)] shrink-0"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <DialogTitle
              id={titleId}
              className="text-sm font-semibold text-[var(--color-secondary)] font-headline"
            >
              Tool Approval Required
            </DialogTitle>
            <DialogDescription id={descId} className="text-xs text-[var(--color-muted)] truncate">
              Agent <span className="font-mono">{agentId}</span> is requesting permission to run a tool.
            </DialogDescription>
          </div>
          {queueLength > 1 && (
            <span className="shrink-0 text-[10px] bg-[var(--color-surface-2)] text-[var(--color-muted)] px-2 py-0.5 rounded-full">
              +{queueLength - 1} more
            </span>
          )}
        </DialogHeader>

        {/* Tool info */}
        <div className="px-5 py-4 space-y-3">
          <div>
            <p className="text-xs text-[var(--color-muted)] mb-1">Tool</p>
            <p className="font-mono text-sm text-[var(--color-accent)] font-semibold">
              {toolName}
            </p>
          </div>

          {bashPreview && (
            <div>
              <p className="text-xs text-[var(--color-muted)] mb-1">Command</p>
              <pre className="font-mono text-xs bg-[var(--color-surface-2)] rounded-lg px-3 py-2 whitespace-pre-wrap break-all text-[var(--color-secondary)]">
                {bashPreview.envPrefix && (
                  <span className="text-[var(--color-muted)]">{bashPreview.envPrefix} </span>
                )}
                <span className="text-[var(--color-accent)] font-semibold">{bashPreview.binary}</span>
                <span>{bashPreview.args}</span>
              </pre>
              {bashCwd && (
                <p className="mt-1 text-[10px] text-[var(--color-muted)]">
                  <span className="text-[var(--color-border)]">dir: </span>
                  <span className="font-mono">{bashCwd}</span>
                </p>
              )}
            </div>
          )}

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
              <XCircle size={13} weight="fill" aria-hidden="true" />
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

        {/* Action buttons.
            Approve/Deny carry equal visual weight as the primary decision.
            Always Allow is a de-emphasized (ghost) secondary action — it
            approves this call AND records a session-scoped grant, mirroring
            the retired ExecApprovalBlock's "Always Allow" ghost button (same
            Lock icon + muted styling) — see the ADR-036 note atop this file.
            Cancel stays last and right-aligned (ml-auto) as the least common
            action. flex-wrap keeps all four usable at phone widths (<768px)
            without any button clipping. */}
        {!hasExpired && (
          <div className="flex flex-wrap gap-2 px-5 py-4 border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
            <Button
              size="sm"
              variant="default"
              onClick={() => handleAction('approve')}
              disabled={submitting}
              className="h-8 text-xs flex-1 sm:flex-none"
            >
              <CheckCircle size={14} weight="bold" aria-hidden="true" />
              Approve
            </Button>
            <Button
              ref={denyButtonRef}
              size="sm"
              variant="outline"
              onClick={() => handleAction('deny')}
              disabled={submitting}
              className="h-8 text-xs flex-1 sm:flex-none"
            >
              <XCircle size={14} weight="bold" aria-hidden="true" />
              Deny
            </Button>
            <Button
              size="sm"
              variant="ghost"
              data-testid="always-allow-toggle"
              onClick={() => handleAction('always')}
              disabled={submitting}
              className="h-8 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] flex-1 sm:flex-none"
            >
              <Lock size={14} aria-hidden="true" />
              Always Allow
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => handleAction('cancel')}
              disabled={submitting}
              className="h-8 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] ml-auto"
            >
              <ProhibitInset size={14} aria-hidden="true" />
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
      </DialogContent>
    </Dialog>
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
