/**
 * toolResultSentinels — shared structured-failure-sentinel detection for
 * ToolCallBadge.tsx and GenericToolCall.tsx.
 *
 * F7 (second review wave on branch fix/615-617-618-hardening): before this
 * file existed, ToolCallBadge.tsx and GenericToolCall.tsx each independently
 * carried THREE `isXxx(result) ? result : null` detections, a visibility
 * disjunction over all three, and a byte-identical amber
 * `statusDot('bg-[var(--color-warning)]')` status-config branch per
 * sentinel — six edits across two files for any fourth sentinel. The
 * evidence that duplication bites: `permission_denied` (issue #618) shipped
 * with ZERO SPA detector at all, because nobody had one place to add it.
 *
 * This module is the ONE place that detects the three structured-failure
 * sentinels (delegation-denied, file-exists refusal, permission-denied) and
 * derives their shared amber status-config. It does NOT decide precedence —
 * that stays with each caller, because the two callers order it differently
 * on purpose:
 *   - ToolCallBadge puts sentinel detection ABOVE all four ordinary statuses
 *     (running/success/error/cancelled) — a sentinel result always wins.
 *   - GenericToolCall puts isRunning/isCancelled ABOVE sentinel detection —
 *     a still-running or cancelled call never shows a sentinel label even
 *     if `result` happens to already carry one (e.g. a stale/replayed
 *     partial result).
 * Each caller reads `sentinels.statusConfig` (non-null only when a sentinel
 * matched) at the point in its own if/else-if chain where it wants sentinel
 * precedence to apply.
 *
 * GenericToolCall also still needs the individual `delegationFailure`/
 * `fileExistsRefusal`/`permissionDenied` objects beyond just the status
 * config — for `PermissionDeniedDisplay`, `fileExistsRefusal.reason`, and
 * excluding a matched sentinel from the plain-JSON-result fallback — so the
 * helper returns those individually, not just the derived config.
 */

import type {
  DelegationFailure,
  FileExistsRefusal,
  PermissionDenied,
} from '@/lib/api/generated/asyncapi-types'
import { PermissionDenied as PermissionDeniedSchema } from '@/lib/api/generated/schemas'
import { statusDot, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'

/**
 * Returns true when the result is the structured delegation-denied sentinel the
 * backend emits when a delegation tool call is refused by policy. Matched
 * against the generated DelegationFailure contract (error: "delegation_denied").
 */
export function isDelegationFailure(value: unknown): value is DelegationFailure {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['error'] === 'delegation_denied'
  )
}

/**
 * Detects the structured write_file precondition refusal (ADR-059 W5,
 * error: "file_exists" — the generated FileExistsRefusal contract).
 *
 * Needed for the same reason isDelegationFailure is: without a detector the
 * payload falls through to a plain-JSON-result rendering. That applies on
 * REPLAY specifically, because live chat routes write_file to its own
 * registered UI (FileWriteConfirm) while replay routes it through
 * GenericToolCall/ToolCallBadge.
 */
export function isFileExistsRefusal(value: unknown): value is FileExistsRefusal {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['error'] === 'file_exists' &&
    typeof (value as Record<string, unknown>)['reason'] === 'string'
  )
}

/**
 * Detects the structured permission-denied sentinel (issue #618, the generated
 * PermissionDenied contract, error: "permission_denied"). Covers ONE backend
 * producer, not two: `pkg/tools/fserrors.go`'s filesystem-scope denial path
 * emits this shape as a tool CALL RESULT, which lands in `session.ToolCall`
 * and reaches this detector. `pkg/agent`'s tool-policy denial path does NOT —
 * it emits `EventKindToolExecSkipped`, which the gateway's event switch never
 * translates into a `ToolCall` (only `ToolExecStart`/`ToolExecEnd` are
 * handled, and `ToolExecStart` only fires once the denial gate has already
 * been passed); that denial is LLM-facing only, surfaced to the calling agent
 * via `pkg/agent/approval_transcript.go`'s own human-facing `denied`
 * transcript entry, a separate path this detector never sees. Constraint #8
 * (contract-first wire formats): validated via the generated Zod schema
 * (`PermissionDeniedSchema`, `.strict()`, all five fields required) rather
 * than a hand-rolled field check, so this stays in sync with the contract by
 * construction instead of silently drifting from it (the drift that let
 * #618 ship with permission_denied having no SPA detector at all).
 */
export function isPermissionDenied(value: unknown): value is PermissionDenied {
  return PermissionDeniedSchema.safeParse(value).success
}

/** Human label for the policy axis that blocked a delegation. */
export function policyAxisLabel(policy: DelegationFailure['policy']): string {
  switch (policy) {
    case 'trust_set':
      return 'Trust set'
    case 'mode':
      return 'Delegation mode'
    case 'depth':
      return 'Delegation depth'
    default:
      return policy
  }
}

export interface ToolResultSentinels {
  delegationFailure: DelegationFailure | null
  fileExistsRefusal: FileExistsRefusal | null
  permissionDenied: PermissionDenied | null
  /** True when any of the three sentinels matched — the shared visibility/error disjunct. */
  any: boolean
  /**
   * The shared amber status config for whichever sentinel matched (delegation
   * failure > file-exists refusal > permission-denied, matching the order
   * every prior caller used), or null when none matched. Callers splice this
   * into their OWN if/else-if chain at the point where they want sentinel
   * precedence to apply — see this module's doc comment for why that point
   * differs between ToolCallBadge and GenericToolCall.
   */
  statusConfig: ToolBadgeStatusConfig | null
}

/** Detects all three structured-failure sentinels on a tool-call result and derives their shared status config. */
export function detectToolResultSentinels(result: unknown): ToolResultSentinels {
  const delegationFailure = isDelegationFailure(result) ? result : null
  const fileExistsRefusal = isFileExistsRefusal(result) ? result : null
  const permissionDenied = isPermissionDenied(result) ? result : null

  let statusConfig: ToolBadgeStatusConfig | null = null
  if (delegationFailure) {
    // No equivalent status in getToolBadgeStatusConfig's 4-value domain (see
    // toolStatusConfig.tsx's file header) — reuses the shared `statusDot`
    // helper so its dot matches the other four statuses exactly.
    statusConfig = {
      indicator: statusDot('bg-[var(--color-warning)]'),
      label: `Delegation denied · ${policyAxisLabel(delegationFailure.policy)}`,
    }
  } else if (fileExistsRefusal) {
    // A precondition refusal is NOT an I/O failure — the file is simply
    // already there, which is frequently the correct outcome when a sibling
    // agent got there first. Labelling it "Failed" is the same conflation the
    // backend half of W5 removed for the calling agent.
    statusConfig = {
      indicator: statusDot('bg-[var(--color-warning)]'),
      label: 'File already exists',
    }
  } else if (permissionDenied) {
    // issue #618: same treatment as delegationFailure/fileExistsRefusal — a
    // distinct label instead of a generic "Failed".
    statusConfig = {
      indicator: statusDot('bg-[var(--color-warning)]'),
      label: 'Permission denied',
    }
  }

  return {
    delegationFailure,
    fileExistsRefusal,
    permissionDenied,
    any: !!delegationFailure || !!fileExistsRefusal || !!permissionDenied,
    statusConfig,
  }
}
