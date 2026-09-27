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
// Buttons — wire action values renamed by ADR-092 D4:
//   Approve Once → POST /api/v1/tool-approvals/{id} {action:"allow_once"}
//   Always Allow → POST /api/v1/tool-approvals/{id} {action:"allow", scope}
//   Deny         → POST /api/v1/tool-approvals/{id} {action:"deny"}
//   Cancel       → POST /api/v1/tool-approvals/{id} {action:"cancel"}
// "Approve" was relabelled "Approve Once" (review finding, post-D4) — as a
// backend lane makes allow_once record NO grant, "Approve" alone read as
// ambiguous next to "Always Allow"; the label must say one-time on its face.
// The action mapping itself (Approve Once → allow_once, Always Allow →
// allow) was already correct and is unchanged.
//
// ADR-092 D4 status: the wire-value rename (L0: approve→allow_once,
// always→allow) landed first so the component compiled against the current
// ToolApprovalActionRequest enum. This pass adds the two pieces that were
// still open:
//   - Grant scope (FR-024): for a shell (`bash`) command only, a RadioGroup
//     ("Allow this exact command" / "Allow commands starting with <server-
//     suggested prefix>") appears above the buttons whenever Always Allow is
//     offered; the selected scope is sent on `allow` only (omitted for every
//     other action/tool, matching the contract's default). The "prefix"
//     option only ever appears once the server has actually supplied a
//     suggested prefix (CommandSegmentInfo.suggested_prefix) — never guessed
//     client-side.
//   - Per-segment display (FR-025/FR-027): when the frame carried
//     ToolApprovalRequiredFrame.segments (a chained command the D3 matcher
//     could not fully resolve), each segment's command text is shown
//     separately with its resolved binary highlighted, in the generic
//     (non-`replace`-mode) tool-info area.
// The four-button layout (including the rendered Cancel button) is
// UNCHANGED by this pass — D4's UI-presentation note describing a
// [Deny][Allow once][Allow]-only layout with no rendered Cancel button is
// not implemented here; `cancel` already behaves as a distinct wire value
// from `deny` per FR-023, which is the part of that note this modal's own
// resolution test (ToolApprovalModal.resolution.test.tsx) depends on.
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
//     Allow") is now fully available here too — the Allow button posts
//     {action:"allow"}, which the gateway resolves by approving the call AND
//     recording a session-scoped grant via ApprovalGrantStore.Record
//     (pkg/gateway/rest_tool_registry.go, commit 35447760). The wire contract
//     (ToolApprovalActionRequest.action) carries "allow" for every tool, not
//     just bash — closing the gap that used to make grant-inheritance
//     (agent-delegation-spec.md FR-D8) reachable only via the retired
//     exec-only flow.

import { useEffect, useState, useCallback, useRef, useMemo } from 'react'
import { CheckCircle, XCircle, ProhibitInset, Shield, Lock, WarningCircle } from '@phosphor-icons/react'
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
import { useToolApprovalStore, isApprovalInScope } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { submitToolApproval, isApiError, fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'
import type { ToolApprovalActionRequest } from '@/lib/api/generated/openapi-types'
import type { CommandSegmentInfo } from '@/lib/api/generated/asyncapi-types'
import { useUiStore } from '@/store/ui'
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
  /**
   * Present on every entry sourced from a live tool_approval_required frame
   * (the wire schema requires both non-empty, minLength 1). Empty here is the
   * "reconnect stub" signal — see isReconnectStub below and the Deliverable 4
   * note on ToolApprovalModal's call site.
   */
  toolCallId: string
  turnId: string
  sessionId: string
  /**
   * ADR-092 D4 — per-segment breakdown of a chained shell command, present
   * only when the server could not fully resolve every segment. Undefined
   * (not just empty) means "no breakdown to show" — see PendingToolApproval's
   * own doc comment (src/store/toolApproval.ts).
   */
  segments?: CommandSegmentInfo[]
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
  segments,
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
              {isReconnectStub ? 'Approval Details Unavailable' : dialogTitleText}
            </DialogTitle>
            <DialogDescription id={descId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] truncate">
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
            <span className="shrink-0 text-[length:var(--type-caption-size)] bg-[var(--color-surface-2)] text-[var(--color-muted)] px-[var(--space-2)] py-[var(--space-0-5)] rounded-full">
              +{queueLength - 1} more
            </span>
          )}
        </DialogHeader>

        {/* Tool info — reconnect-stub notice, a 'replace'-mode readable summary
            (e.g. request_mount), or the generic Tool line + optional
            'additive' preview (e.g. bash) + raw Arguments JSON fallback. */}
        {isReconnectStub ? (
          <div className="px-[var(--space-3)] py-[var(--space-3)] space-y-[var(--space-2)]">
            <div className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]">
              <WarningCircle size={16} weight="bold" className="shrink-0 mt-[var(--space-0-5)]" aria-hidden="true" />
              <p>
                This page reconnected after {humanizeToolName(toolName)} was already waiting on a
                decision, and the original request details did not come back with it.
              </p>
            </div>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              Denying is the safe choice when you can&apos;t see what&apos;s being asked.
            </p>
          </div>
        ) : replaceEntry ? (
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
        {!hasExpired && !isReconnectStub && !hideAlwaysAllow && isShellCommand && (
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
          <div className="flex flex-wrap gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-3)] border-t border-[var(--color-border)] bg-[var(--color-surface-2)]">
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
                className="h-8 text-[length:var(--type-utility-xs-size)] w-full"
              >
                <XCircle size={14} weight="bold" aria-hidden="true" />
                Deny
              </Button>
            ) : (
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
                  ref={denyButtonRef}
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
            )}
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
      segments={first.segments}
    />
  )
}
