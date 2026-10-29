// ToolApprovalPanel — the inline tool-approval card (omnipus-system-agent K7;
// L1 amendment §1.2 R6, §1.5; #1263; FR-011, FR-082, FR-052, FR-073; ADR-092 D4).
//
// Replaces the app-wide ToolApprovalModal. An approval renders INLINE, docked
// above the composer, in the chat it belongs to: the root of the acting
// session's chain (`approvalChatId` = root_session_id, else the acting
// session). A helper's approval therefore appears in its parent chat — an
// Omnipus helper's in the Omnipus chat, whatever workspace is active.
// There is no "waiting in another chat" notice (founder ruling 2026-10-18):
// the parent main's sidebar attention cue is the signal elsewhere.
//
// Safety floor (amendment R6, binding):
//   - The card never takes focus when it appears, and no action is a default
//     Enter target: typing in the composer never resolves it.
//   - Escape does not resolve it (Stop already cancels pending approvals).
//   - It resolves only through POST /api/v1/tool-approvals/{approval_id} on an
//     explicit click: no auto-submit, no grace timer; a timeout is the
//     server's deny (shown as expired here).
//   - The composer stays unlocked.
//
// Actions — wire values from ADR-092 D4:
//   Approve Once → {action:"allow_once"}; Always Allow → {action:"allow", scope}
//   (bash: exact or the server-suggested prefix); Deny → {action:"deny"};
//   Cancel → {action:"cancel"}.
//
// Error handling: 401 → forced logout; 403 → admin-only toast; 404/410 → the
// approval is no longer pending (markResolved; allow/allow_once also warns
// that it was not applied).
//
// Reload: the session_state snapshot carries the full request (bundle B3), so
// a restored entry is as complete and approvable as a live one.

import { useEffect, useState, useCallback, useMemo } from 'react'
import { ArrowSquareOut, CheckCircle, XCircle, ProhibitInset, Shield, Lock, WarningCircle } from '@phosphor-icons/react'
import { useRouter } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { approvalChatId, useToolApprovalStore, type PendingToolApproval } from '@/store/toolApproval'
import { submitToolApproval, isApiError, fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'
import type { ToolApprovalActionRequest } from '@/lib/api/generated/openapi-types'
import type { CommandSegmentInfo } from '@/lib/api/generated/asyncapi-types'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { forceLogout } from '@/lib/authLogout'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { queryClient } from '@/lib/queryClient'
import { TOOL_APPROVAL_PREVIEWS } from './approvalPreviews/registry'
import type { ToolApprovalPreviewContext } from './approvalPreviews/types'
import { highlightBinaryInText } from './approvalPreviews/BashApprovalPreview'

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
  /** K7: the acting agent's display name (a helper names itself). */
  actingAgentName?: string
  /** K7 / §6.18: server-derived turn origin, shown for a non-web origin. */
  originLabel?: string
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
  actingAgentName,
  originLabel,
}: ToolApprovalCardProps) {
  const dequeue = useToolApprovalStore((s) => s.dequeue)
  const markResolved = useToolApprovalStore((s) => s.markResolved)
  const addToast = useUiStore((s) => s.addToast)
  const router = useRouter({ warn: false })
  const viewedSessionId = useSessionStore((s) => s.activeSessionId)
  const [submitting, setSubmitting] = useState(false)
  const { remainingMs, progressPct } = useCountdown(expiresAt)

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
        // ADR-092 D4/FR-024: scope is only meaningful (and only sent) when
        // this action IS the grant-recording "allow" AND the approval is a
        // shell command — every other action/tool combination calls
        // submitToolApproval with its two-argument signature (no `scope` at
        // all, not even an explicit `undefined` third argument) so the wire
        // body has no `scope` key, matching the contract's own "ignored
        // otherwise" note without relying on the server to ignore a stray
        // value.
        const resp =
          action === 'allow' && isShellCommand
            ? await submitToolApproval(approvalId, action, scope)
            : await submitToolApproval(approvalId, action)
        if (action === 'allow') {
          if (resp.grant_recorded !== true) {
            addToast({
              message: 'This call is allowed, but Always Allow did not stick. The next identical call will ask again.',
              variant: 'warning',
            })
          } else if (resp.scope) {
            // ADR-092 D4/FR-024: resp.scope is the scope the server actually
            // RECORDED, not a passthrough of the local `scope` selection —
            // show that, not the client's own request, so a server-side
            // downgrade (e.g. no prefix suggestion survived a race) is never
            // silently different from what the operator is told stuck.
            addToast({
              message:
                resp.scope === 'prefix'
                  ? 'Always allowed — every command starting with that prefix is now allowed for this session.'
                  : 'Always allowed — this exact command is now allowed for this session.',
              variant: 'success',
            })
          }
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
            if (action === 'allow_once' || action === 'allow') {
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
    [approvalId, dequeue, markResolved, addToast, submitting, isShellCommand, scope],
  )

  const argsJson = JSON.stringify(args, null, 2)
  const titleId = `tool-approval-title-${approvalId}`
  const descId = `tool-approval-desc-${approvalId}`

  const mountPath =
    typeof args.host_path === 'string' ? args.host_path.trim()
    : typeof args.path === 'string' ? args.path.trim()
    : ''
  // Always Allow remembers the exact arguments. A request_mount with no
  // folder path has nothing safe to remember — hide the button.
  const hideAlwaysAllow = toolName === 'request_mount' && !mountPath

  // ── Per-tool readable-summary registry (Deliverables 1-3) ─────────────────
  // Agent display name (D-87): a permission prompt is exactly where the human
  // needs to know WHICH agent is asking, and a raw UUID is not that. The
  // ['agents'] cache is usually warm (the sidebar and chat both populate it);
  // when it is not — this dialog can open on any screen — fetch it once and
  // re-render. The id remains the fallback so the prompt is never blank.
  const cachedAgentName = queryClient.getQueryData<Agent[]>(['agents'])?.find((a) => a.id === agentId)?.name
  const [fetchedAgentName, setFetchedAgentName] = useState<string | undefined>(undefined)
  useEffect(() => {
    // K7: a server-supplied acting_agent_name already names the actor — no
    // roster read is needed for it.
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
  const escalation = replaceEntry ? null : describeEscalation(args)
  const primaryLabel = replaceEntry?.primaryLabel ?? 'Approve Once'
  const secondaryLabel = replaceEntry?.secondaryLabel ?? 'Deny'
  const showCancelButton = replaceEntry ? (replaceEntry.showCancel ?? true) : true

  return (
    // Inline, not a dialog (amendment R6): no focus trap, no focus on
    // appearance, no Escape resolution — only an explicit click decides.
    <section
      data-testid="tool-approval-panel"
      aria-labelledby={titleId}
      aria-describedby={descId}
      // Train 3 gate F05: height-bounded column. The header and the decision
      // row stay fixed; everything between them scrolls inside the card, so
      // a long script or segment list never pushes Approve/Deny (or the
      // composer below) out of reach.
      className="my-[var(--space-2)] flex max-h-[60vh] flex-col overflow-hidden rounded-[var(--radius-medium)] border border-[var(--color-warning)]/40 bg-[var(--color-surface-1)]"
    >
        {/* Header */}
        <div className="flex shrink-0 flex-row items-center gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-3)] border-b border-[var(--color-border)] text-left">
          <Shield
            size={20}
            weight="bold"
            className="text-[var(--color-warning)] shrink-0"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <h3
              id={titleId}
              className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)] font-headline"
            >
              {dialogTitleText}
            </h3>
            <p id={descId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] truncate">
              {replaceEntry ? (
                'Review the details below before deciding.'
              ) : (
                <>
                  <span className="font-medium text-[var(--color-secondary)]">{resolvedAgentName}</span>{' '}
                  is requesting permission to run a tool.
                </>
              )}
            </p>
            {originLabel && (
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid="tool-approval-origin">
                {originLabel}
              </p>
            )}
          </div>
          {queueLength > 1 && (
            <span className="shrink-0 text-[length:var(--type-caption-size)] bg-[var(--color-surface-2)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-0-5)] rounded-full">
              +{queueLength - 1} more
            </span>
          )}
        </div>

        <div data-testid="tool-approval-details" className="min-h-0 flex-1 overflow-y-auto">
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

        {/* ADR-092 D4/FR-024: grant scope choice — only for a shell command
            with an Always Allow button on offer. "exact" (command text + cwd)
            is always available and is the default; "prefix" ({binary,
            arg_prefix}, ignores cwd, token-boundary matched) only appears
            once the server has actually suggested one (prefixScopeAvailable)
            — never a client-guessed prefix. Applies to every currently
            unmatched segment shown above (one choice, not one per segment). */}
        {!hasExpired && !hideAlwaysAllow && isShellCommand && (
          <div className="px-[var(--space-3)] pb-[var(--space-2-5)]">
            <RadioGroup
              aria-label="Grant scope for Always Allow"
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
          <div className="flex shrink-0 flex-wrap gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-3)] border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
            <>
                <Button
                  size="sm"
                  variant="default"
                  onClick={() => handleAction('allow_once')}
                  disabled={submitting}
                  className="h-8 text-[length:var(--type-utility-xs-size)] flex-1 sm:flex-none"
                >
                  <CheckCircle size={14} weight="bold" aria-hidden="true" />
                  {primaryLabel}
                </Button>
                <Button
                    size="sm"
                  variant="outline"
                  onClick={() => handleAction('deny')}
                  disabled={submitting}
                  className="h-8 text-[length:var(--type-utility-xs-size)] flex-1 sm:flex-none"
                >
                  <XCircle size={14} weight="bold" aria-hidden="true" />
                  {secondaryLabel}
                </Button>
                {!hideAlwaysAllow && (
                <Button
                  size="sm"
                  variant="ghost"
                  data-testid="always-allow-toggle"
                  onClick={() => handleAction('allow')}
                  disabled={submitting}
                  className="h-8 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] flex-1 sm:flex-none"
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
          <div className="shrink-0 px-[var(--space-3)] py-[var(--space-3)] border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
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
    </section>
  )
}

/**
 * The approval for the chat that is open: the front approval whose root chat
 * (`approvalChatId`) is the active session. Approvals for other chats stay
 * queued and are announced by the "waiting in another chat" banner.
 */
export function ToolApprovalPanel({ roomId }: { roomId?: string } = {}) {
  const queue = useToolApprovalStore((s) => s.queue)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  // #1449 FR-048/FR-051 (D8, D23): on a group page only the cards that carry
  // THIS group's reference; the same card also shows in the agent's own
  // chat, and one decision (by approval id) resolves both.
  const visible: PendingToolApproval[] = roomId
    ? queue.filter((a) => a.roomRef?.room_id === roomId)
    : activeSessionId
      ? queue.filter((a) => approvalChatId(a) === activeSessionId)
      : []
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
      actingAgentName={first.actingAgentName}
      originLabel={first.originLabel}
    />
  )
}

/** Docked mount above the composer (next to the question and secret cards). */
export function ToolApprovalThreadTail({ roomId }: { roomId?: string } = {}) {
  return (
    <div className="mx-auto w-full max-w-3xl px-[var(--space-3)]" data-testid="tool-approval-thread-tail">
      <ToolApprovalPanel roomId={roomId} />
    </div>
  )
}
