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
// Buttons — exactly the server's offered choices (Skills & Tools C-24/C-26,
// rendered by ApprovalCard.tsx::ApprovalChoices), each a
// POST /api/v1/tool-approvals/{id}:
//   Approve Once            → {action:"allow_once"}
//   Allow for 1h / 3h / 1d  → {action:"allow_for", duration[, scope for bash]}
//   Approve this version…   → {action:"approve_version"} / "approve_version_allow_for"
//   Deny                    → {action:"deny"}
//   Cancel                  → {action:"cancel"}
// No card offers a lasting allow: that exists only as stored policy. The five
// exact-action tools and always-ask tools never get a time choice. After a
// timed choice the toast reports what the server stored (expires_at), never
// what was asked.
//
// Grant scope (FR-024): for a shell (`bash`) command with time choices on
// offer, a RadioGroup ("Allow this exact command" / "Allow commands starting
// with <server-suggested prefix>") says what the time limit covers; the
// selected scope is sent on timed actions only. The "prefix" option appears
// only once the server supplied CommandSegmentInfo.suggested_prefix.
// Per-segment display (FR-025/FR-027): when the frame carried segments, each
// segment's command text is shown separately with its binary highlighted.
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
//         card goes and cannot come back; allow_once/allow also gets a
//         warning that it was not applied
//
// Dismissal: Cancel and Close/Escape/overlay remove the card from this tab
// FIRST and unconditionally, then send their request — a failing request can
// never leave a dialog the user cannot get rid of. Server-made resolutions
// (another tab, timeout, Stop, agent deletion) arrive as tool_approval_resolved
// frames and are applied by src/store/chat.ts → markResolved.
//
// Scope (L1 stage 1): every pending approval is shown, whatever its workspace
// or chat — see ToolApprovalModal below. Release showed only the active
// workspace's approvals (isApprovalInScope) plus a cross-workspace banner.
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
//   - The exec flow's lasting "Always Allow" is retired (C-24): a card now
//     offers only one-request and time-limited choices.

import { useEffect, useState, useCallback, useRef, useMemo } from 'react'
import { ArrowSquareOut, XCircle, ProhibitInset, Shield, WarningCircle } from '@phosphor-icons/react'
import { useRouter } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { useToolApprovalStore } from '@/store/toolApproval'
import { submitToolApproval, isApiError, fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'
import type { ToolApprovalActionRequest } from '@/lib/api/generated/openapi-types'
import type { CommandSegmentInfo } from '@/lib/api/generated/asyncapi-types'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { forceLogout } from '@/lib/authLogout'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { isAlwaysAskTool, ONE_REQUEST_APPROVAL_NOTICE } from '@/lib/alwaysAskTools'
import { queryClient } from '@/lib/queryClient'
import { TOOL_APPROVAL_PREVIEWS } from './approvalPreviews/registry'
import type { ToolApprovalPreviewContext } from './approvalPreviews/types'
import { highlightBinaryInText } from './approvalPreviews/BashApprovalPreview'
import {
  ApprovalChoices,
  timedChoiceResult,
  refusedAnswerMessage,
  versionApprovedResult,
  type ApprovalAction,
  type ApprovalChoice,
  type ApprovalDuration,
} from './ApprovalCard'

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

// ── ADR-092 D7/D8 Auto pre-flight escalations, and the D3 rule-ask prompt ──
// A tool call that hits an Auto-mode pre-flight escalation (D7 filesystem,
// D8 network) carries the reason in its own args, not in a separate wire
// field — pkg/tools/shell_permission_mode.go's requestPreflightApproval
// sets args.adr092_kind ("fs_preflight" | "fs_preflight_blind" |
// "network_preflight") and args.note (a human-readable sentence already
// built server-side). Without this, the card looked exactly like an
// ordinary "ask"-policy bash approval, with no hint that Auto had already
// tried and failed to clear the call itself.
//
// The D3 "rule_ask" kind is a DIFFERENT thing — an ordinary operator
// command-rule ask, not an Auto escalation (nothing tried and failed to
// clear the call; an operator rule simply requires a human look at this
// command). pkg/agent/loop_policy.go::ruleAskRequestArgs sets adr092_kind
// "rule_ask" together with a note naming the matched rule (e.g. `matches an
// operator rule that requires approval (binary="rm" arg_prefix="rm -rf")`)
// whenever the call matched a genuine ask rule — so rule_ask DOES carry a
// note, same as the escalation kinds, and describeEscalation shows it
// through the same banner with its own, non-escalation headline below.
const ESCALATION_HEADLINES: Record<string, string> = {
  fs_preflight: 'This command wants to reach outside the workspace.',
  fs_preflight_blind: "This command's filesystem reach could not be classified from its text alone.",
  network_preflight: 'This command wants network access.',
  rule_ask: 'An operator command rule requires approval for this command.',
}

function describeEscalation(args: Record<string, unknown>): { headline: string; detail: string } | null {
  const kind = args.adr092_kind
  const note = args.note
  if (typeof kind !== 'string' || typeof note !== 'string' || note.length === 0) return null
  return {
    headline: ESCALATION_HEADLINES[kind] ?? 'This command needs more access than the sandbox currently grants.',
    detail: note,
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
  sessionId: string
  /**
   * ADR-092 D4 — per-segment breakdown of a chained shell command, present
   * only when the server could not fully resolve every segment. Undefined
   * (not just empty) means "no breakdown to show" — see PendingToolApproval's
   * own doc comment (src/store/toolApproval.ts).
   */
  segments?: CommandSegmentInfo[]
  /** K7 / FR-251: server-derived turn origin, retained across reconnects. */
  originLabel?: string
  /** K7: display name of the acting agent (a helper names itself). */
  actingAgentName?: string
  /** C-24/C-26: the server-computed choices of this card. */
  offeredActions: readonly ApprovalAction[]
  allowDurations: readonly ApprovalDuration[]
  /** C-9: an unverified skill install under Ask has one install choice. */
  skillUnverifiedInstall?: boolean
  /** C-9: the skill this card loads or installs, for the result line. */
  skillName?: string
}

function ToolApprovalCard({
  approvalId,
  toolName,
  args,
  agentId,
  expiresAt,
  queueLength,
  sessionId,
  segments,
  originLabel,
  actingAgentName,
  offeredActions,
  allowDurations,
  skillUnverifiedInstall,
  skillName,
}: ToolApprovalCardProps) {
  const dequeue = useToolApprovalStore((s) => s.dequeue)
  const markResolved = useToolApprovalStore((s) => s.markResolved)
  const addToast = useUiStore((s) => s.addToast)
  const router = useRouter({ warn: false })
  const viewedSessionId = useSessionStore((s) => s.activeSessionId)
  const [submitting, setSubmitting] = useState(false)
  const { remainingMs, progressPct } = useCountdown(expiresAt)
  // Default-focus target: the Deny button (the safe default). Focusing Deny
  // means a stray Enter on open denies rather than approves.
  const denyButtonRef = useRef<HTMLButtonElement>(null)

  const hasExpired = remainingMs <= 0

  // ── ADR-092 D4: grant scope (exact vs. prefix) ────────────────────────────
  // Only a shell command has a meaningful "prefix" grant (a resolved
  // {binary, arg_prefix} pair) — every other tool's Allow keeps recording an
  // exact-arguments grant exactly as before, so `scope` is never sent for
  // them (submitToolApproval omits the key entirely; the contract's own
  // documented default is "exact"). "exact" is always a legal choice for a
  // shell command too, and is the safer default (narrowest grant), so it is
  // selected on open regardless of whether a prefix suggestion exists.
  const isShellCommand = toolName === 'bash' && typeof args.command === 'string' && args.command.length > 0
  const [scope, setScope] = useState<NonNullable<ToolApprovalActionRequest['scope']>>('exact')

  // A prefix grant needs a server-suggested prefix to offer (FR-026's
  // stop-token algorithm runs server-side, in CommandSegmentInfo.suggested_prefix
  // — this UI never guesses one client-side). Absent segments (a non-chained
  // command the server hasn't emitted per-segment info for, or a build that
  // doesn't populate it yet), only "exact" is offered — never a fabricated
  // prefix. When multiple unmatched segments each offer a suggestion, the one
  // scope choice covers all of them (D4: "one rule per segment" is a
  // server-side recording detail, not a per-segment client choice), so the
  // label lists every distinct suggested prefix.
  const suggestedPrefixes = useMemo(
    () =>
      Array.from(
        new Set(
          (segments ?? [])
            .filter((s) => s.prefix_available && s.suggested_prefix)
            .map((s) => s.suggested_prefix as string),
        ),
      ),
    [segments],
  )
  const prefixScopeAvailable = isShellCommand && suggestedPrefixes.length > 0
  // If a later frame update (or the card being re-shown after a queue
  // re-render) drops the prefix suggestion out from under an already-selected
  // "prefix" choice, fall back to the always-legal "exact" rather than
  // silently sending a scope the UI no longer shows a description for.
  useEffect(() => {
    if (!prefixScopeAvailable && scope === 'prefix') setScope('exact')
  }, [prefixScopeAvailable, scope])

  // ── Per-tool readable-summary registry (Deliverables 1-3) ─────────────────
  // Agent display name (D-87): a permission prompt is exactly where the human
  // needs to know WHICH agent is asking, and a raw UUID is not that. The
  // ['agents'] cache is usually warm (the sidebar and chat both populate it);
  // when it is not — this dialog can open on any screen — fetch it once and
  // re-render. The id remains the fallback so the prompt is never blank.
  const cachedAgentName = queryClient.getQueryData<Agent[]>(['agents'])?.find((a) => a.id === agentId)?.name
  const [fetchedAgentName, setFetchedAgentName] = useState<string | undefined>(undefined)
  useEffect(() => {
    // K7: a server-supplied acting_agent_name already names the actor.
    if (cachedAgentName || actingAgentName) return
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
  }, [agentId, cachedAgentName, actingAgentName])
  const resolvedAgentName = actingAgentName || cachedAgentName || fetchedAgentName || agentId

  // dismissFirst: take the card off THIS tab before the request goes out.
  // Cancel and Close/Escape/overlay pass it — they must dismiss locally
  // whatever the server answers. If the server still holds the approval
  // open, the next session_state snapshot restores it (the agent is still
  // waiting), so nothing is lost by dismissing early.
  const handleAction = useCallback(
    async (choice: ApprovalChoice, opts?: { dismissFirst?: boolean }) => {
      if (submitting) return
      setSubmitting(true)
      if (opts?.dismissFirst) dequeue(approvalId)
      const { action, duration } = choice
      const timed = action === 'allow_for' || action === 'approve_version_allow_for'
      try {
        // ADR-092 D4/FR-024: a bash card's time limit keeps its narrow scope;
        // every other body carries only the action and, when timed, its
        // duration.
        const resp = await submitToolApproval(approvalId, action, {
          ...(timed && duration ? { duration } : {}),
          ...(timed && isShellCommand ? { scope } : {}),
        })
        if (timed) {
          // C-24/C-26: report what the server stored, never what was asked.
          addToast(timedChoiceResult(resp, resolvedAgentName))
        } else {
          // C-24: a stored version approval says so; Approve Once never does.
          const approved = versionApprovedResult(resp, skillName)
          if (approved) addToast(approved)
        }
        // The server resolved it: remember that, rather than only hiding it.
        markResolved(approvalId)
      } catch (err) {
        if (isApiError(err)) {
          if (err.status === 401) {
            // D9 fix: this POST bypasses React Query entirely, so route
            // through the same forced-logout path every other 401 uses.
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
            // No longer pending on the server: the card goes and stays gone.
            markResolved(approvalId)
            if (action !== 'deny' && action !== 'cancel') {
              addToast({
                message: 'This request was already closed before your approval arrived, so your approval was not applied.',
                variant: 'warning',
              })
            }
          } else {
            addToast({
              message: refusedAnswerMessage(err.status, err.body) ?? `Failed to submit approval: ${err.userMessage}`,
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
    [approvalId, dequeue, markResolved, addToast, submitting, isShellCommand, scope, resolvedAgentName, skillName],
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
      void handleAction({ action: 'deny' }, { dismissFirst: true })
    }
  }, [submitting, hasExpired, dequeue, approvalId, handleAction])

  const titleId = `tool-approval-title-${approvalId}`
  const descId = `tool-approval-desc-${approvalId}`

  // C-24/C-26: the card offers exactly the server's choices. An always-ask
  // tool answers one request (channels FR-038); exact-action tools
  // (request_mount among them) never get a time choice.
  const alwaysAsk = isAlwaysAskTool(toolName)
  const offered = offeredActions
  const timeChoicesOffered = offered.includes('allow_for') || offered.includes('approve_version_allow_for')

  const previewEntry = TOOL_APPROVAL_PREVIEWS[toolName]
  const replaceEntry = previewEntry?.mode === 'replace' ? previewEntry : undefined
  // The card shows only the entry's redacted copy (summary and raw dump alike).
  const shownArgs = previewEntry?.redactArgs ? previewEntry.redactArgs(args) : args
  const argsJson = JSON.stringify(shownArgs, null, 2)
  const previewCtx: ToolApprovalPreviewContext = {
    toolName,
    args: shownArgs,
    agentId,
    agentName: resolvedAgentName,
    sessionId,
  }
  const dialogTitleText = replaceEntry
    ? (replaceEntry.title?.(previewCtx) ?? 'Tool Approval Required')
    : 'Tool Approval Required'
  const escalation = replaceEntry ? null : describeEscalation(args)
  const primaryLabel = replaceEntry?.primaryLabel ?? 'Approve Once'
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
        aria-describedby={originLabel ? `${descId} ${descId}-origin` : descId}
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
        <DialogHeader className="flex flex-row items-center gap-[var(--space-2-5)] space-y-0 px-[var(--space-3)] py-[var(--space-3)] border-b border-[var(--color-border)] text-left">
          <Shield
            size={20}
            weight="bold"
            className="text-[var(--color-warning)] shrink-0"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <DialogTitle
              id={titleId}
              className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)] font-headline"
            >
              {dialogTitleText}
            </DialogTitle>
            <DialogDescription id={descId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] truncate">
              {replaceEntry ? (
                'Review the details below before deciding.'
              ) : (
                <>
                  <span className="font-medium text-[var(--color-secondary)]">{resolvedAgentName}</span>{' '}
                  is requesting permission to run a tool.
                </>
              )}
            </DialogDescription>
            {originLabel && (
              <DialogDescription
                id={`${descId}-origin`}
                className="break-words text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
                data-testid="tool-approval-origin"
              >
                {originLabel}
              </DialogDescription>
            )}
          </div>
          {queueLength > 1 && (
            <span className="shrink-0 text-[length:var(--type-caption-size)] bg-[var(--color-surface-2)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-0-5)] rounded-full">
              +{queueLength - 1} more
            </span>
          )}
        </DialogHeader>

        {/* FR-034: a helper or run asked, not the chat in front of the user.
            Names where it is acting and opens that run's own session. */}
        {sessionId && sessionId !== viewedSessionId && (
          <div className="flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)] border-b border-[var(--color-border)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            <span className="flex-1 min-w-0 truncate">Asked from a helper or run in another session.</span>
            <Button
              type="button"
              variant="link"
              size="sm"
              data-testid="approval-open-run"
              data-session-id={sessionId}
              className="h-auto shrink-0 gap-[var(--space-1)] px-0 py-0 text-[length:var(--type-utility-xs-size)]"
              onClick={() => {
                if (!router) {
                  addToast({ message: "Can't open that run from here.", variant: 'warning' })
                  return
                }
                void router.navigate({ to: '/sessions/$sessionId', params: { sessionId } })
              }}
            >
              <ArrowSquareOut size={11} aria-hidden="true" />
              Open run
            </Button>
          </div>
        )}

        {/* Tool info — a 'replace'-mode readable summary
            (e.g. request_mount), or the generic Tool line + optional
            'additive' preview (e.g. bash) + raw Arguments JSON fallback. */}
        {replaceEntry ? (
          <replaceEntry.Body {...previewCtx} />
        ) : (
          <div className="px-[var(--space-3)] py-[var(--space-3)] space-y-[var(--space-2-5)]">
            {escalation && (
              <div
                data-testid="auto-escalation-explanation"
                className="flex items-start gap-[var(--space-2)] rounded-lg border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)]"
              >
                <WarningCircle size={16} weight="bold" className="shrink-0 mt-[var(--space-0-5)] text-[var(--color-warning)]" aria-hidden="true" />
                <div className="space-y-[var(--space-0-5)]">
                  <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-warning)]">
                    {escalation.headline}
                  </p>
                  <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]/80">
                    {escalation.detail}
                  </p>
                </div>
              </div>
            )}

            <div>
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mb-[var(--space-1)]">Tool</p>
              <p className="font-mono text-[length:var(--type-body-compact-size)] text-[var(--color-accent)] font-semibold">
                {humanizeToolName(toolName)}
              </p>
            </div>

            {previewEntry && <previewEntry.Body {...previewCtx} />}

            {segments && segments.length > 0 && (
              <div data-testid="command-segments">
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mb-[var(--space-1)]">
                  {segments.length > 1
                    ? 'Chained command — each part needs approval'
                    : 'Command part needing approval'}
                </p>
                <ul className="space-y-[var(--space-2)]">
                  {segments.map((segment) => {
                    const { before, binary, after } = highlightBinaryInText(
                      segment.command_text,
                      segment.resolved_binary,
                    )
                    return (
                      <li
                        key={segment.segment_index}
                        data-testid={`command-segment-${segment.segment_index}`}
                        className="rounded-lg bg-[var(--color-surface-2)] px-[var(--space-2-5)] py-[var(--space-2)]"
                      >
                        <div className="flex items-center justify-between gap-[var(--space-2)]">
                          {segments.length > 1 && (
                            <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                              Part {segment.segment_index + 1}
                            </span>
                          )}
                          <div className="flex items-center gap-[var(--space-1)] ml-auto">
                            {segment.classification && segment.classification !== 'none' && (
                              <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] bg-[var(--color-surface-1)] px-[var(--space-2)] py-[var(--space-0-5)] rounded-full">
                                {segment.classification === 'read_write' ? 'read + write' : segment.classification}
                              </span>
                            )}
                            {segment.network_required && (
                              <span className="text-[length:var(--type-caption-size)] text-[var(--color-warning)] bg-[var(--color-surface-1)] px-[var(--space-2)] py-[var(--space-0-5)] rounded-full">
                                network
                              </span>
                            )}
                          </div>
                        </div>
                        <pre className="mt-[var(--space-1)] font-mono text-[length:var(--type-utility-xs-size)] whitespace-pre-wrap break-all text-[var(--color-secondary)]">
                          {before}
                          <span className="text-[var(--color-accent)] font-semibold">{binary}</span>
                          {after}
                        </pre>
                      </li>
                    )
                  })}
                </ul>
              </div>
            )}

            {args && Object.keys(args).length > 0 && (
              <div>
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mb-[var(--space-1)]">Arguments</p>
                <pre className="text-[length:var(--type-utility-xs-size)] font-mono bg-[var(--color-surface-2)] rounded-lg px-[var(--space-2-5)] py-[var(--space-2)] overflow-auto max-h-40 whitespace-pre-wrap break-all text-[var(--color-secondary)]">
                  {argsJson}
                </pre>
              </div>
            )}
          </div>
        )}

        {/* Channels FR-038 / S-29: an always-ask tool's approval covers only this call. */}
        {!hasExpired && alwaysAsk && (
          <p className="px-[var(--space-3)] pb-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]">
            {ONE_REQUEST_APPROVAL_NOTICE}
          </p>
        )}

        {/* Countdown */}
        <div className="px-[var(--space-3)] pb-[var(--space-2-5)]">
          {hasExpired ? (
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)] flex items-center gap-[var(--space-1)]">
              <XCircle size={13} weight="fill" aria-hidden="true" />
              Approval expired unanswered — the agent is told nobody answered (a timeout, not a denial by you).
            </p>
          ) : (
            <>
              <div className="flex items-center justify-between mb-[var(--space-1)]">
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Expires in</p>
                <p className="text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-secondary)]">
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

        {/* ADR-092 D4/FR-024: what a bash card's time limit covers — only
            for a shell command with time choices on offer. "exact" is the
            default; "prefix" appears only once the server suggested one. */}
        {!hasExpired && timeChoicesOffered && isShellCommand && (
          <div className="px-[var(--space-3)] pb-[var(--space-2-5)]">
            <RadioGroup
              aria-label="What a time limit covers"
              value={scope}
              onValueChange={(next) => setScope(next as NonNullable<ToolApprovalActionRequest['scope']>)}
              orientation="vertical"
            >
              <RadioGroupItem value="exact" data-testid="scope-exact">
                Allow this exact command
              </RadioGroupItem>
              {prefixScopeAvailable && (
                <RadioGroupItem value="prefix" data-testid="scope-prefix">
                  Allow commands starting with{' '}
                  {suggestedPrefixes.map((p, i) => (
                    <span key={p}>
                      {i > 0 && ', '}
                      <span className="font-mono text-[var(--color-accent)]">{p}</span>
                    </span>
                  ))}
                </RadioGroupItem>
              )}
            </RadioGroup>
          </div>
        )}

        {/* Action buttons: exactly the server's offered choices (C-24).
            Cancel stays last and right-aligned (ml-auto). flex-wrap keeps
            every choice usable at phone widths. */}
        {!hasExpired && (
          <div className="flex flex-wrap gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-3)] border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
            <>
                <ApprovalChoices
                  offered={offered}
                  durations={allowDurations}
                  agentName={resolvedAgentName}
                  onceLabel={primaryLabel}
                  denyLabel={secondaryLabel}
                  installApproval={skillUnverifiedInstall}
                  disabled={submitting}
                  denyRef={denyButtonRef}
                  onChoose={(choice) => void handleAction(choice)}
                />
                {showCancelButton && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => void handleAction({ action: 'cancel' }, { dismissFirst: true })}
                    disabled={submitting}
                    className="h-8 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] ml-auto"
                  >
                    <ProhibitInset size={14} aria-hidden="true" />
                    Cancel
                  </Button>
                )}
              </>
          </div>
        )}

        {hasExpired && (
          <div className="px-[var(--space-3)] py-[var(--space-3)] border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => dequeue(approvalId)}
              className="h-8 text-[length:var(--type-utility-xs-size)] w-full text-[var(--color-muted)]"
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
  // L1 stage 1 (founder, 2026-10-14): there is no "waiting elsewhere" banner,
  // so every pending approval — any workspace, any chat, the open one too —
  // is shown here; none can be lost. (Release scoped this to the active
  // workspace and announced the rest in a cross-workspace banner.)
  const visible = queue
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
      sessionId={first.sessionId}
      segments={first.segments}
      originLabel={first.originLabel}
      actingAgentName={first.actingAgentName}
      offeredActions={first.offeredActions}
      allowDurations={first.allowDurations}
      skillUnverifiedInstall={first.toolName === 'install_skill' && first.skill?.unverified === true}
      skillName={first.skill?.name}
    />
  )
}
