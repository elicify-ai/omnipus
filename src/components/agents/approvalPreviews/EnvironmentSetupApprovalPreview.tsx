// Readable summary for `environment_setup` tool-approval requests — GENERIC
// INSTALL (GENERIC-INSTALL-DECISION.md, option A; ES spec ES-FR-01/02).
//
// The agent supplies the actual installation command or inline script; the
// approver reads THAT text, the stated purpose, the resolved destination
// workspace and the installation scope — "The command/script itself and
// destination are visible in the normal approval display" (ES-FR-02). There
// is no dependency list any more: dependency knowledge belongs to skills and
// the task plan, never to this preview. The command block reuses the bash
// preview's formatting pattern (formatBashCommand) so an installation reads
// exactly like the shell call it is.
//
// Registered as a 'replace'-mode registry entry (registry.ts): the raw
// argument names (command/scope/target_workspace) are jargon the fields
// below translate in full — the same rationale as request_mount. No custom
// button labels are set: the modal's STANDARD Approve/Deny/Always
// Allow/Cancel row stays (lane constraint: no new approval workflow, standard
// semantics preserved).
//
// Fidelity guard: this card hides the raw JSON dump (replace mode), so it
// may only do so when it summarizes EVERYTHING the request carries — per
// BRANCH, because the two cards render disjoint subsets of the known keys:
// a poll/read/kill carrying extra known install fields (a command riding on
// a poll), a run carrying session_id, an out-of-enum scope string — each
// re-reveals the raw JSON alongside the summary. Any key outside the tool
// schema or a malformed known key (non-string command, unknown action verb,
// non-numeric timeout) does the same — request content is never silently
// dropped.
//
// Target workspace precedence (GS-06 disposition: Admin may explicitly
// select a destination workspace it has authority over, without membership):
// explicit `target_workspace` in the request → session-derived workspace →
// "Unknown workspace". Showing the session's workspace when an explicit
// different target was requested would tell the approver the WRONG
// destination.

import { Terminal, WarningCircle } from '@phosphor-icons/react'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'
import { formatBashCommand } from './BashApprovalPreview'
import type { ToolApprovalPreviewContext } from './types'

type EffectiveScope = 'shared' | 'workspace'

/** poll/read/kill manage an existing background session; anything else starts one. */
function isSessionAction(action: string): boolean {
  return action === 'poll' || action === 'read' || action === 'kill'
}

/** 'shared' (any casing) is the explicit, weightier scope; everything else — including absent — is the workspace default (ES-FR-02). */
function normalizeScope(value: unknown): EffectiveScope {
  return typeof value === 'string' && value.trim().toLowerCase() === 'shared' ? 'shared' : 'workspace'
}

function firstNonEmptyString(source: Record<string, unknown>, key: string): string {
  const value = source[key]
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : ''
}

/**
 * Keys the environment_setup tool schema actually defines (Parameters() in
 * pkg/tools/environment_setup.go — generic install: see
 * .local/adr090/environment-setup-delivery/generic-frontend-interface.md)
 * — the summary's fidelity boundary. Anything outside this set is content
 * the readable summary does NOT cover.
 */
const KNOWN_ARGUMENT_KEYS = new Set([
  'action',
  'command',
  'purpose',
  'scope',
  'target_workspace',
  'session_id',
  'timeout_seconds',
])

/** The action verbs the preview can describe. Anything else falls to the raw-JSON fallback. */
const KNOWN_ACTIONS = new Set(['run', 'poll', 'read', 'kill'])

/** Scope is enum-backed (tool schema: `workspace` | `shared`); matched case-insensitively like the shared check. */
const VALID_SCOPES = new Set(['shared', 'workspace'])

/**
 * Fidelity guard (approval-critical): does the readable summary cover
 * EVERYTHING in the raw request, as the branch that renders would display
 * it? Coverage is BRANCH-AWARE (MAJ-001): the session card shows only the
 * action + session id — the install card never shows session_id — so a key
 * being known and well-typed is not enough; it must also be something the
 * rendered card actually displays. Also false when any key outside the tool
 * schema appears, when a present known key carries content the summary
 * cannot render readably (a non-string command, an unknown action verb, a
 * non-numeric timeout), or when scope is present but not one of the tool's
 * enum values (an unsupported scope must never be presented as the valid
 * workspace default). Absent keys are fine — missing content renders as an
 * honest "not included" line instead. On false, the raw JSON renders
 * alongside the summary — request content is never silently dropped in
 * replace mode (the generic dump is hidden there, so the summary is the
 * approver's only window).
 */
function summarizesWholeRequest(args: Record<string, unknown>): boolean {
  // Which card will render? Same predicate as the render switch below.
  const rawAction = typeof args.action === 'string' ? args.action.trim().toLowerCase() : ''
  const sessionCard = isSessionAction(rawAction)

  for (const key of Object.keys(args)) {
    if (!KNOWN_ARGUMENT_KEYS.has(key)) return false

    if (sessionCard) {
      // Session card renders only the action description + session id.
      // Any other present key — e.g. a command riding on a poll — is
      // request content the summary would hide from the approver.
      if (key !== 'action' && key !== 'session_id') return false
      if (key === 'session_id' && typeof args.session_id !== 'string') return false
      continue
    }

    switch (key) {
      case 'command':
        // Present but unusable (wrong type, or an empty string the tool
        // would never send on purpose): surface the raw request.
        if (typeof args.command !== 'string' || args.command.trim() === '') return false
        break
      case 'action':
        if (!KNOWN_ACTIONS.has(rawAction)) return false
        break
      case 'purpose':
      case 'target_workspace':
        if (typeof args[key] !== 'string') return false
        break
      case 'scope':
        // Enum-backed (the tool rejects anything but workspace|shared): an
        // unknown scope string must fall back to the raw request, or the
        // scope line would present an unsupported value as the workspace
        // default.
        if (typeof args.scope !== 'string') return false
        if (!VALID_SCOPES.has(args.scope.trim().toLowerCase())) return false
        break
      case 'session_id':
        // The install card never renders a session id: one riding on a run
        // request is unrendered request content — surface the raw request.
        return false
      case 'timeout_seconds':
        if (typeof args.timeout_seconds !== 'number' || !Number.isFinite(args.timeout_seconds) || args.timeout_seconds <= 0) return false
        break
    }
  }
  return true
}

/** Poll/read/kill target a started installation; the card says which. */
function sessionActionDescription(action: string): string {
  if (action === 'kill') return 'Stop a background installation and release its locks.'
  if (action === 'read') return 'Read the output of a background installation started earlier.'
  return 'Check on a background installation started earlier.'
}

/**
 * Plain-language timeout: whole minutes as "5m", otherwise honest seconds
 * ("45s", "125s") — never an invented rounding.
 */
function formatTimeout(seconds: number): string {
  return seconds >= 60 && seconds % 60 === 0 ? `${seconds / 60}m` : `${seconds}s`
}

/**
 * The modal's DialogTitle for environment_setup approvals. Varies by action
 * (exported because registry entries carry `title` as a function of context;
 * button labels are static strings and stay standard).
 */
export function environmentSetupApprovalTitle(ctx: ToolApprovalPreviewContext): string {
  const action = typeof ctx.args.action === 'string' ? ctx.args.action.trim().toLowerCase() : ''
  if (action === 'kill') return `${ctx.agentName} wants to stop an installation`
  if (isSessionAction(action)) return `${ctx.agentName} wants to check an installation`
  return `${ctx.agentName} wants to install software`
}

/**
 * Best-effort workspace label. Read-only `queryClient.getQueryData`
 * lookups against the SAME `['sessions']` / `workspacesQueryKeys.list(...)`
 * cache entries the Sidebar already populates — not a fresh `useQuery`
 * subscription (same pattern as RequestMountApprovalPreview).
 */
function resolveWorkspaceLabel(args: Record<string, unknown>, sessionId: string): string {
  const workspaces = queryClient.getQueryData<Workspace[]>(
    workspacesQueryKeys.list({ status: 'active' }),
  )
  const nameFor = (id: string): string => workspaces?.find((w) => w.id === id)?.name ?? id

  // 1. An explicit target in the request wins — an Admin (or any caller with
  //    cross-workspace authority) may aim the install at a workspace other
  //    than the one this chat session belongs to. The approver must see THE
  //    requested destination, never the session's by default.
  const explicitTarget = firstNonEmptyString(args, 'target_workspace')
  if (explicitTarget !== '') return nameFor(explicitTarget)

  // 2. No explicit target: the job installs into the caller's own turn
  //    workspace. Resolve the session's workspace the same way request_mount
  //    does.
  const sessions = queryClient.getQueryData<Session[]>(['sessions'])
  const workspaceId = sessions?.find((s) => s.id === sessionId)?.workspace_id
  if (workspaceId) return nameFor(workspaceId)

  return 'Unknown workspace'
}

export function EnvironmentSetupApprovalPreview({ args, sessionId }: ToolApprovalPreviewContext) {
  const rawAction = typeof args.action === 'string' ? args.action.trim().toLowerCase() : ''
  const knownAction = rawAction === '' || KNOWN_ACTIONS.has(rawAction)
  const sessionIdArg = firstNonEmptyString(args, 'session_id')
  const fullySummarized = summarizesWholeRequest(args)
  const argsJson = JSON.stringify(args, null, 2)

  // Fidelity block — shown whenever the readable summary does not cover the
  // whole request. Renders in BOTH branches: unknown content can ride along
  // on a poll/read/kill call just as on an install.
  const fidelityBlock = fullySummarized ? null : (
    <div>
      <p className="text-xs text-[var(--color-warning)] mb-1 flex items-center gap-1">
        <WarningCircle size={13} weight="bold" aria-hidden="true" />
        Some details of this request are not summarized above
      </p>
      <pre className="text-xs font-mono bg-[var(--color-surface-2)] rounded-lg px-3 py-2 overflow-auto max-h-40 whitespace-pre-wrap break-all text-[var(--color-secondary)]">
        {argsJson}
      </pre>
    </div>
  )

  // ── poll/read/kill: session management, not a new install ────────────────
  if (isSessionAction(rawAction) && knownAction) {
    return (
      <div className="px-5 py-4 space-y-4">
        <div>
          <p className="text-xs text-[var(--color-muted)] mb-1">Action</p>
          <p className="text-sm text-[var(--color-secondary)]">{sessionActionDescription(rawAction)}</p>
        </div>
        <div>
          <p className="text-xs text-[var(--color-muted)] mb-1">Session</p>
          {sessionIdArg !== '' ? (
            <p className="font-mono text-sm text-[var(--color-secondary)] break-all">{sessionIdArg}</p>
          ) : (
            <p className="text-sm text-[var(--color-error)]">
              No session was included with this request.
            </p>
          )}
        </div>
        {fidelityBlock}
      </div>
    )
  }

  // ── install: the command itself, purpose, workspace, scope ───────────────
  const command = typeof args.command === 'string' && args.command.trim() !== '' ? args.command : ''
  const preview = formatBashCommand(command)
  const purpose = firstNonEmptyString(args, 'purpose')
  const scope = normalizeScope(args.scope)
  const timeoutSeconds = typeof args.timeout_seconds === 'number' && Number.isFinite(args.timeout_seconds) && args.timeout_seconds > 0
    ? args.timeout_seconds
    : null
  const workspaceLabel = resolveWorkspaceLabel(args, sessionId)

  return (
    <div className="px-5 py-4 space-y-4">
      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1 flex items-center gap-1">
          <Terminal size={13} aria-hidden="true" />
          Command
        </p>
        {command !== '' ? (
          <pre
            data-testid="environment-setup-command"
            className="font-mono text-xs bg-[var(--color-surface-2)] rounded-lg px-3 py-2 whitespace-pre-wrap break-all text-[var(--color-secondary)]"
          >
            {preview.envPrefix && (
              <span className="text-[var(--color-muted)]">{preview.envPrefix} </span>
            )}
            <span className="text-[var(--color-accent)] font-semibold">{preview.binary}</span>
            <span>{preview.args}</span>
          </pre>
        ) : (
          <p className="text-sm text-[var(--color-error)]">
            No installation command was included with this request.
          </p>
        )}
      </div>

      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1">Why</p>
        <p className="text-sm text-[var(--color-secondary)]">
          {purpose !== '' ? purpose : 'No reason was given.'}
        </p>
      </div>

      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1">Workspace</p>
        <p className="font-mono text-sm font-medium text-[var(--color-secondary)] break-all">
          {workspaceLabel}
        </p>
      </div>

      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1">Scope</p>
        <p className="text-sm text-[var(--color-secondary)]">{scope === 'shared' ? 'Shared' : 'This workspace'}</p>
      </div>

      {timeoutSeconds !== null && (
        <div>
          <p className="text-xs text-[var(--color-muted)] mb-1">Timeout</p>
          <p className="text-sm font-mono text-[var(--color-secondary)]">{formatTimeout(timeoutSeconds)}</p>
        </div>
      )}

      {fidelityBlock}

      {scope === 'shared' && (
        <p className="text-xs text-[var(--color-warning)] pt-3 border-t border-[var(--color-border)]">
          Shared components are installed once by Omnipus, kept outside the workspace, and stay
          available to every agent.
        </p>
      )}
    </div>
  )
}
