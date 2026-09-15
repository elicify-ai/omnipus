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
//   404 / 410 → the approval is no longer pending server-side (410 inside the
//         registry's retention window, 404 after it) → markResolved: the
//         card goes and cannot come back; an approve/always also gets a
//         warning that it was not applied
//
// Dismissal: Cancel and Close/Escape/overlay remove the card from this tab
// FIRST and unconditionally, then send their request — a failing request can
// never leave a dialog the user cannot get rid of. Server-made resolutions
// (another tab, timeout, Stop, agent deletion) arrive as tool_approval_resolved
// frames and are applied by src/store/chat.ts → markResolved.
//
// Scope: only approvals whose workspace matches the active workspace (or that
// have no workspace) are shown — see isApprovalInScope. Out-of-scope approvals
// stay queued and appear when the user switches to their workspace.
//
// ADR-036 §3.4 note: this is now the ONLY tool-approval UI — the dedicated
// exec-only flow (ExecApprovalBlock/ExecApprovalTool, WS
// exec_approval_request/response/expired frames) was retired in favor of
// this generic modal for every tool, `bash` included. Before deleting that
// flow, its rendering was compared against this one:
//   - PORTED: the readable "binary highlighted, env-prefix separated"
//     command preview + working-dir line ExecApprovalBlock showed for shell
//     commands. Now lives in approvalPreviews/BashApprovalPreview.tsx,
//     registered under the 'bash' key in approvalPreviews/registry.ts as an
//     'additive' entry — rendered whenever toolName is "bash" and
//     args.command is a string, in addition to (not instead of) the generic
//     Arguments JSON dump. See the per-tool preview registry note below.
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
import { CheckCircle, XCircle, ProhibitInset, Shield, Lock, WarningCircle } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { useToolApprovalStore, isApprovalInScope } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { submitToolApproval, isApiError, fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { forceLogout } from '@/lib/authLogout'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { queryClient } from '@/lib/queryClient'
import { TOOL_APPROVAL_PREVIEWS } from './approvalPreviews/registry'
import type { ToolApprovalPreviewContext } from './approvalPreviews/types'

function useCountdown(expiresAt: number): { remainingMs: number; progressPct: number; totalMs: number } {
  const [remainingMs, setRemainingMs] = useState(() => Math.max(0, expiresAt - Date.now()))
  // Capture the total duration once so the progress bar doesn't jump
  const [totalMs] = useState(() => Math.max(1, expiresAt - Date.now()))

  useEffect(() => {
    const tick = () => {
      const left = Math.max(0, expiresAt - Date.now())
      setRemainingMs(left)
      return left
    }
    tick()
    const interval = setInterval(() => {
      if (tick() === 0) clearInterval(interval)
    }, 500)
    // Browsers throttle timers in a background tab to as little as once a
    // minute, so the displayed countdown lags real time while the tab is
    // hidden (UAT 2026-09-13 D-16 observed 52 s of countdown over 180 s of
    // wall-clock). expiresAt is an absolute local timestamp, so one tick on
    // return to the foreground snaps the display back to the truth.
    const onVisible = () => {
      if (document.visibilityState === 'visible') tick()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', onVisible)
    }
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
  /**
   * Present on every entry sourced from a live tool_approval_required frame
   * (the wire schema requires both non-empty, minLength 1). Empty here is the
   * "reconnect stub" signal — see isReconnectStub below and the Deliverable 4
   * note on ToolApprovalModal's call site.
   */
  toolCallId: string
  turnId: string
  sessionId: string
}

function ToolApprovalCard({
  approvalId,
  toolName,
  args,
  agentId,
  expiresAt,
  queueLength,
  toolCallId,
  turnId,
  sessionId,
}: ToolApprovalCardProps) {
  const dequeue = useToolApprovalStore((s) => s.dequeue)
  const markResolved = useToolApprovalStore((s) => s.markResolved)
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
    //
    // dismissFirst: take the card off THIS tab before the request goes out.
    // Cancel and Close/Escape/overlay pass it — they must dismiss locally
    // whatever the server answers. If the server still holds the approval
    // open, the next session_state snapshot restores it (the agent is still
    // waiting), so nothing is lost by dismissing early.
    async (action: Parameters<typeof submitToolApproval>[1], opts?: { dismissFirst?: boolean }) => {
      if (submitting) return
      setSubmitting(true)
      if (opts?.dismissFirst) dequeue(approvalId)
      try {
        const resp = await submitToolApproval(approvalId, action)
        if (action === 'always' && resp.grant_recorded !== true) {
          addToast({
            message: 'This call is allowed, but Always Allow did not stick. The next identical call will ask again.',
            variant: 'warning',
          })
        }
        // The server resolved it: remember that, rather than only hiding it.
        markResolved(approvalId)
      } catch (err) {
        if (isApiError(err)) {
          if (err.status === 401) {
            // D9 fix: this POST bypasses React Query entirely (raw await), so
            // queryClient.ts's global 401 subscriber never saw it — the rest
            // of the app (Sidebar's "logged in as X", every other open
            // screen) kept looking logged-in while this modal alone knew the
            // session was dead. Route through the same forced-logout path
            // every other 401 uses instead of a toast-only dead end.
            addToast({
              message: 'Session expired — please log in again to approve tool calls.',
              variant: 'error',
            })
            forceLogout()
          } else if (err.status === 403) {
            addToast({
              message: 'You must be an admin to approve this tool.',
              variant: 'error',
            })
          } else if (err.status === 404 || err.status === 410) {
            // No longer pending on the server. 410 = resolved within the
            // registry's terminal-retention window; 404 = resolved longer
            // ago than that (the entry was purged) or never existed. Nothing
            // is left to decide, so the card must go and stay gone — before
            // this, a 404 only toasted and left a dialog whose every button
            // (Close included) re-sent a request that could only 404 again.
            markResolved(approvalId)
            if (action === 'approve' || action === 'always') {
              // Deny/cancel stay silent (what the user asked for holds or no
              // longer matters). An approval that did not land deserves a word.
              addToast({
                message: 'This request was already closed before your approval arrived, so your approval was not applied.',
                variant: 'warning',
              })
            }
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
    [approvalId, dequeue, markResolved, addToast, submitting],
  )

  // Safe-default handler for Escape / overlay-click / X close. The Dialog
  // primitive requests a close; we translate that into the SAFE decision:
  //   - not expired  → dismiss the card locally AND submit a DENY (never an
  //     approve, never a silent dismiss that leaves the agent hanging). The
  //     local dismissal is unconditional: a close must always close, even
  //     when the deny request fails.
  //   - expired      → the decision is already made server-side; just dismiss
  //     the notice from the local queue.
  const handleDismissRequest = useCallback(() => {
    if (submitting) return
    if (hasExpired) {
      dequeue(approvalId)
    } else {
      void handleAction('deny', { dismissFirst: true })
    }
  }, [submitting, hasExpired, dequeue, approvalId, handleAction])

  const argsJson = JSON.stringify(args, null, 2)
  const titleId = `tool-approval-title-${approvalId}`
  const descId = `tool-approval-desc-${approvalId}`

  // ── Reconnect-gap guard (Deliverable 4) ───────────────────────────────────
  // SessionStatePendingApproval (the WS session_state reconnect snapshot)
  // carries no `args`, `tool_call_id`, or `turn_id` — strictly less than a
  // live tool_approval_required frame. When an approval the server still
  // considers pending was never seen by this tab as a live frame (page
  // reload while an approval was outstanding, or a second tab connecting
  // afterwards), reconcileWithSessionState (src/store/toolApproval.ts) adds a
  // stub queue entry for it with toolCallId/turnId set to '' — see that
  // function's own comment for the full rationale. This is a safe,
  // collision-free signal rather than a heuristic: toolCallId and turnId are
  // both required + minLength 1 on the wire (ToolApprovalRequiredFrame
  // schema), so a genuine live frame — the only thing enqueue() ever
  // constructs a queue entry from — can never produce an empty string here.
  const isReconnectStub = toolCallId === '' && turnId === ''
  const mountPath =
    typeof args.host_path === 'string' ? args.host_path.trim()
    : typeof args.path === 'string' ? args.path.trim()
    : ''
  // Always Allow remembers the exact arguments. A request_mount with no
  // folder path has nothing safe to remember — hide the button, same as a
  // reconnect stub.
  const hideAlwaysAllow = isReconnectStub || (toolName === 'request_mount' && !mountPath)

  // ── Per-tool readable-summary registry (Deliverables 1-3) ─────────────────
  // Agent display name (D-87): a permission prompt is exactly where the human
  // needs to know WHICH agent is asking, and a raw UUID is not that. The
  // ['agents'] cache is usually warm (the sidebar and chat both populate it);
  // when it is not — this dialog can open on any screen — fetch it once and
  // re-render. The id remains the fallback so the prompt is never blank.
  const cachedAgentName = queryClient.getQueryData<Agent[]>(['agents'])?.find((a) => a.id === agentId)?.name
  const [fetchedAgentName, setFetchedAgentName] = useState<string | undefined>(undefined)
  useEffect(() => {
    if (cachedAgentName) return
    let cancelled = false
    queryClient
      .ensureQueryData({ queryKey: ['agents'], queryFn: fetchAgents })
      .then((agents) => {
        if (cancelled) return
        const name = agents.find((a) => a.id === agentId)?.name
        if (name) setFetchedAgentName(name)
      })
      .catch(() => {
        /* the id fallback below stands */
      })
    return () => {
      cancelled = true
    }
  }, [agentId, cachedAgentName])
  const resolvedAgentName = cachedAgentName || fetchedAgentName || agentId
  const previewEntry = TOOL_APPROVAL_PREVIEWS[toolName]
  const replaceEntry = previewEntry?.mode === 'replace' ? previewEntry : undefined
  const previewCtx: ToolApprovalPreviewContext = {
    toolName,
    args,
    agentId,
    agentName: resolvedAgentName,
    sessionId,
  }
  const dialogTitleText = replaceEntry
    ? (replaceEntry.title?.(previewCtx) ?? 'Tool Approval Required')
    : 'Tool Approval Required'
  const primaryLabel = replaceEntry?.primaryLabel ?? 'Approve'
  const secondaryLabel = replaceEntry?.secondaryLabel ?? 'Deny'
  const showCancelButton = replaceEntry ? (replaceEntry.showCancel ?? true) : true

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
              {isReconnectStub ? 'Approval Details Unavailable' : dialogTitleText}
            </DialogTitle>
            <DialogDescription id={descId} className="text-xs text-[var(--color-muted)] truncate">
              {isReconnectStub ? (
                "This page can't show what's being asked — see below."
              ) : replaceEntry ? (
                'Review the details below before deciding.'
              ) : (
                <>
                  <span className="font-medium text-[var(--color-secondary)]">{resolvedAgentName}</span>{' '}
                  is requesting permission to run a tool.
                </>
              )}
            </DialogDescription>
          </div>
          {queueLength > 1 && (
            <span className="shrink-0 text-[10px] bg-[var(--color-surface-2)] text-[var(--color-muted)] px-2 py-0.5 rounded-full">
              +{queueLength - 1} more
            </span>
          )}
        </DialogHeader>

        {/* Tool info — reconnect-stub notice, a 'replace'-mode readable summary
            (e.g. request_mount), or the generic Tool line + optional
            'additive' preview (e.g. bash) + raw Arguments JSON fallback. */}
        {isReconnectStub ? (
          <div className="px-5 py-4 space-y-2">
            <div className="flex items-start gap-2 text-sm text-[var(--color-warning)]">
              <WarningCircle size={16} weight="bold" className="shrink-0 mt-0.5" aria-hidden="true" />
              <p>
                This page reconnected after {humanizeToolName(toolName)} was already waiting on a
                decision, and the original request details did not come back with it.
              </p>
            </div>
            <p className="text-xs text-[var(--color-muted)]">
              Denying is the safe choice when you can&apos;t see what&apos;s being asked.
            </p>
          </div>
        ) : replaceEntry ? (
          <replaceEntry.Body {...previewCtx} />
        ) : (
          <div className="px-5 py-4 space-y-3">
            <div>
              <p className="text-xs text-[var(--color-muted)] mb-1">Tool</p>
              <p className="font-mono text-sm text-[var(--color-accent)] font-semibold">
                {humanizeToolName(toolName)}
              </p>
            </div>

            {previewEntry && <previewEntry.Body {...previewCtx} />}

            {args && Object.keys(args).length > 0 && (
              <div>
                <p className="text-xs text-[var(--color-muted)] mb-1">Arguments</p>
                <pre className="text-xs font-mono bg-[var(--color-surface-2)] rounded-lg px-3 py-2 overflow-auto max-h-40 whitespace-pre-wrap break-all text-[var(--color-secondary)]">
                  {argsJson}
                </pre>
              </div>
            )}
          </div>
        )}

        {/* Countdown */}
        <div className="px-5 pb-3">
          {hasExpired ? (
            <p className="text-xs text-[var(--color-error)] flex items-center gap-1">
              <XCircle size={13} weight="fill" aria-hidden="true" />
              Approval expired unanswered — the agent is told nobody answered (a timeout, not a denial by you).
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
            {isReconnectStub ? (
              // Only the safe action is offered. A reconnect stub has no
              // arguments: Always Allow would record a grant for {}, which
              // is not the call the user was asked about. No Approve either
              // — approving a request this page cannot show would be a
              // blind decision.
              <Button
                ref={denyButtonRef}
                size="sm"
                variant="outline"
                onClick={() => handleAction('deny')}
                disabled={submitting}
                className="h-8 text-xs w-full"
              >
                <XCircle size={14} weight="bold" aria-hidden="true" />
                Deny
              </Button>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="default"
                  onClick={() => handleAction('approve')}
                  disabled={submitting}
                  className="h-8 text-xs flex-1 sm:flex-none"
                >
                  <CheckCircle size={14} weight="bold" aria-hidden="true" />
                  {primaryLabel}
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
                  {secondaryLabel}
                </Button>
                {!hideAlwaysAllow && (
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
                )}
                {showCancelButton && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => handleAction('cancel', { dismissFirst: true })}
                    disabled={submitting}
                    className="h-8 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] ml-auto"
                  >
                    <ProhibitInset size={14} aria-hidden="true" />
                    Cancel
                  </Button>
                )}
              </>
            )}
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

// ToolApprovalModal renders the front-of-queue approval that belongs to the
// active workspace, if any. Out-of-scope approvals stay queued (not dropped)
// and surface when the user switches to their workspace.
export function ToolApprovalModal() {
  const queue = useToolApprovalStore((s) => s.queue)
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const visible = queue.filter((a) => isApprovalInScope(a, activeWorkspaceId))
  const first = visible[0]

  if (!first) return null

  return (
    <ToolApprovalCard
      key={first.approvalId}
      approvalId={first.approvalId}
      toolName={first.toolName}
      args={first.args}
      agentId={first.agentId}
      expiresAt={first.expiresAt}
      queueLength={visible.length}
      toolCallId={first.toolCallId}
      turnId={first.turnId}
      sessionId={first.sessionId}
    />
  )
}
